// Package backup dumps the database to object storage every day and
// restores a dump (plan.md §13.4). It runs the PostgreSQL client tools,
// which the production image carries in the server's major version.
package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/MamaShip/Observer/internal/blob"
)

const (
	// Prefix is where dumps live in the bucket, one per UTC day.
	Prefix = "backups/postgres/"
	// Retention is how long dumps are kept.
	Retention = 120 * 24 * time.Hour
)

// Key is the object key of the dump made on day (UTC).
func Key(day time.Time) string { return Prefix + day.UTC().Format("2006-01-02") + ".dump" }

// Alerter tells the operators.
type Alerter interface {
	Alert(ctx context.Context, key, subject, body string)
}

// Config configures the service.
type Config struct {
	DatabaseURL string
	PgDump      string // default "pg_dump"
	PgRestore   string // default "pg_restore"
	TempDir     string // default os.TempDir()
}

// Service makes and prunes backups.
type Service struct {
	cfg     Config
	store   blob.Store
	alerter Alerter
	log     *slog.Logger
	now     func() time.Time
}

// New builds the service; alerter may be nil.
func New(cfg Config, store blob.Store, alerter Alerter, log *slog.Logger) *Service {
	if cfg.PgDump == "" {
		cfg.PgDump = "pg_dump"
	}
	if cfg.PgRestore == "" {
		cfg.PgRestore = "pg_restore"
	}
	return &Service{cfg: cfg, store: store, alerter: alerter, log: log.With("component", "backup"), now: time.Now}
}

// Result describes a finished backup.
type Result struct {
	Key      string
	Size     int64
	Duration time.Duration
}

// Run dumps the database (pg_dump custom format, compressed), uploads it as
// today's backup and deletes the ones past Retention.
func (s *Service) Run(ctx context.Context) (Result, error) {
	start := s.now()
	f, err := os.CreateTemp(s.cfg.TempDir, "observer-*.dump")
	if err != nil {
		return Result{}, err
	}
	defer func() {
		f.Close()
		os.Remove(f.Name())
	}()

	env, err := pgEnv(s.cfg.DatabaseURL)
	if err != nil {
		return Result{}, err
	}
	cmd := exec.CommandContext(ctx, s.cfg.PgDump, "--format=custom", "--no-owner", "--no-privileges")
	cmd.Env = env
	cmd.Stdout = f
	stderr := &limitedBuffer{max: 4096}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return Result{}, fmt.Errorf("pg_dump: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	size, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return Result{}, err
	}
	if size == 0 {
		return Result{}, errors.New("pg_dump wrote nothing")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Result{}, err
	}
	key := Key(start)
	if err := s.store.Put(ctx, key, f, size, "application/octet-stream"); err != nil {
		return Result{}, fmt.Errorf("upload %s: %w", key, err)
	}

	// Delete the dumps that just passed Retention, plus a week before, in
	// case some days' runs were missed.
	for d := 0; d < 7; d++ {
		old := Key(start.Add(-Retention - time.Duration(d)*24*time.Hour))
		if err := s.store.Delete(ctx, old); err != nil && !errors.Is(err, blob.ErrNotFound) {
			s.log.Warn("could not delete old backup", "key", old, "err", err)
		}
	}
	return Result{Key: key, Size: size, Duration: s.now().Sub(start)}, nil
}

// Daily is the periodic job: it runs a backup and alerts the operators if
// it fails.
func (s *Service) Daily(ctx context.Context) error {
	r, err := s.Run(ctx)
	if err != nil {
		s.log.Error("backup failed", "err", err)
		if s.alerter != nil {
			s.alerter.Alert(ctx, "backup-failed", "数据库备份失败", err.Error())
		}
		return err
	}
	s.log.Info("backup done", "key", r.Key, "bytes", r.Size, "duration", r.Duration.Round(time.Millisecond))
	return nil
}

// Restore loads the dump at key into the database at targetURL, which
// must be empty: restoring over live data is never what an operator wants
// by accident.
func (s *Service) Restore(ctx context.Context, key, targetURL string, out io.Writer) error {
	if _, err := pgEnv(targetURL); err != nil {
		return err
	}
	rc, err := s.store.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("download %s: %w", key, err)
	}
	defer rc.Close()
	f, err := os.CreateTemp(s.cfg.TempDir, "observer-restore-*.dump")
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		os.Remove(f.Name())
	}()
	if _, err := io.Copy(f, rc); err != nil {
		return fmt.Errorf("download %s: %w", key, err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	env, err := pgEnv(targetURL)
	if err != nil {
		return err
	}
	// pg_restore needs the database named on the command line (the name
	// only; the password stays in the environment).
	u, _ := url.Parse(targetURL)
	cmd := exec.CommandContext(ctx, s.cfg.PgRestore, "--no-owner", "--no-privileges", "--exit-on-error", "--single-transaction",
		"--dbname="+strings.TrimPrefix(u.Path, "/"), f.Name())
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_restore: %w", err)
	}
	return nil
}

// pgEnv turns a postgres:// URL into the libpq environment for the client
// tools. (libpq does not expand a URL given as PGDATABASE, and on the
// command line the password would show in the process list.)
func pgEnv(databaseURL string) ([]string, error) {
	u, err := url.Parse(databaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return nil, errors.New("database URL must look like postgres://user:password@host:port/name")
	}
	env := os.Environ()
	add := func(k, v string) {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}
	add("PGHOST", u.Hostname())
	add("PGPORT", u.Port())
	add("PGUSER", u.User.Username())
	pw, _ := u.User.Password()
	add("PGPASSWORD", pw)
	add("PGDATABASE", strings.TrimPrefix(u.Path, "/"))
	add("PGSSLMODE", u.Query().Get("sslmode"))
	return env, nil
}

// DailyAt is a River periodic schedule: every day at the given UTC time.
type DailyAt struct{ Hour, Minute int }

// Next implements river.PeriodicSchedule.
func (d DailyAt) Next(t time.Time) time.Time {
	t = t.UTC()
	next := time.Date(t.Year(), t.Month(), t.Day(), d.Hour, d.Minute, 0, 0, time.UTC)
	if !next.After(t) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// limitedBuffer keeps the first max bytes written to it.
type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
