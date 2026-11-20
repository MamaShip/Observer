package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MamaShip/Observer/internal/blob"
)

const dbURL = "postgres://observer:s3cret@db:5432/observer?sslmode=disable"

// script writes an executable shell script and returns its path.
func script(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// A pg_dump that insists on getting the connection from the environment.
const fakeDump = `[ "$PGHOST" = db ] && [ "$PGPORT" = 5432 ] && [ "$PGUSER" = observer ] && [ "$PGPASSWORD" = s3cret ] &&
[ "$PGDATABASE" = observer ] && [ "$PGSSLMODE" = disable ] || { echo "bad environment" >&2; exit 3; }
case "$*" in *s3cret*) echo "password on the command line" >&2; exit 4;; esac
printf 'PGDMP fake %s' "$*"
`

type alerts struct{ subjects []string }

func (a *alerts) Alert(_ context.Context, _, subject, _ string) {
	a.subjects = append(a.subjects, subject)
}

func newService(t *testing.T, dump, restore string) (*Service, blob.Store, *alerts) {
	t.Helper()
	store, err := blob.NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &alerts{}
	s := New(Config{DatabaseURL: dbURL, PgDump: dump, PgRestore: restore, TempDir: t.TempDir()}, store, a, slog.New(slog.DiscardHandler))
	return s, store, a
}

func put(t *testing.T, store blob.Store, key string) {
	t.Helper()
	if err := store.Put(context.Background(), key, strings.NewReader("old"), 3, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
}

func exists(store blob.Store, key string) bool {
	rc, err := store.Get(context.Background(), key)
	if err == nil {
		rc.Close()
	}
	return err == nil
}

func TestRunUploadsAndPrunes(t *testing.T) {
	ctx := context.Background()
	s, store, a := newService(t, script(t, fakeDump), "")
	now := time.Date(2026, 11, 20, 19, 30, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	day := 24 * time.Hour
	keep := []string{Key(now.Add(-Retention + day)), Key(now.Add(-Retention - 7*day))}
	gone := []string{}
	for d := 0; d < 7; d++ {
		gone = append(gone, Key(now.Add(-Retention-time.Duration(d)*day)))
	}
	for _, k := range append(append([]string{}, keep...), gone...) {
		put(t, store, k)
	}

	r, err := s.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r.Key != "backups/postgres/2026-11-20.dump" || r.Size == 0 {
		t.Fatalf("result = %+v", r)
	}
	rc, err := store.Get(ctx, r.Key)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.HasPrefix(b, []byte("PGDMP fake --format=custom")) || int64(len(b)) != r.Size {
		t.Errorf("uploaded %q", b)
	}
	for _, k := range keep {
		if !exists(store, k) {
			t.Errorf("%s deleted, should be kept", k)
		}
	}
	for _, k := range gone {
		if exists(store, k) {
			t.Errorf("%s kept past retention", k)
		}
	}
	if len(a.subjects) != 0 {
		t.Errorf("alerts: %v", a.subjects)
	}
}

func TestDailyAlertsOnFailure(t *testing.T) {
	ctx := context.Background()
	s, store, a := newService(t, script(t, `echo 'pg_dump: error: connection refused' >&2; exit 1`), "")
	err := s.Daily(ctx)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("err = %v", err)
	}
	if len(a.subjects) != 1 || a.subjects[0] != "数据库备份失败" {
		t.Errorf("alerts = %v", a.subjects)
	}
	if exists(store, Key(time.Now())) {
		t.Error("a failed dump was uploaded")
	}

	s, _, _ = newService(t, script(t, `exit 0`), "")
	if _, err := s.Run(ctx); err == nil || !strings.Contains(err.Error(), "nothing") {
		t.Errorf("empty dump: %v", err)
	}
}

func TestRestore(t *testing.T) {
	ctx := context.Background()
	restore := script(t, `[ "$PGDATABASE" = fresh ] && [ "$PGPASSWORD" = s3cret ] || exit 3
case "$*" in *--dbname=fresh*) ;; *) echo "no --dbname" >&2; exit 5;; esac
case "$*" in *s3cret*) echo "password on the command line" >&2; exit 4;; esac
for last; do :; done
echo "restoring $(cat "$last")"`)
	s, store, _ := newService(t, script(t, fakeDump), restore)
	r, err := s.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := s.Restore(ctx, r.Key, "postgres://observer:s3cret@db:5432/fresh", &out); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if !strings.Contains(out.String(), "restoring PGDMP fake") {
		t.Errorf("restore output %q", out.String())
	}
	if err := s.Restore(ctx, "backups/postgres/1999-01-01.dump", "postgres://x@db/fresh", &out); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("missing dump: %v", err)
	}
	if err := s.Restore(ctx, r.Key, "host=db dbname=fresh", &out); err == nil {
		t.Error("non-URL target accepted")
	}
	_ = store
}

func TestPgEnv(t *testing.T) {
	env, err := pgEnv("postgresql://u:p%40ss@h:6543/d?sslmode=require")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(env, "\n")
	for _, want := range []string{"PGHOST=h", "PGPORT=6543", "PGUSER=u", "PGPASSWORD=p@ss", "PGDATABASE=d", "PGSSLMODE=require"} {
		if !strings.Contains(got, want) {
			t.Errorf("env lacks %s", want)
		}
	}
	if _, err := pgEnv("mysql://u@h/d"); err == nil {
		t.Error("mysql URL accepted")
	}
}

func TestDailyAt(t *testing.T) {
	d := DailyAt{Hour: 19, Minute: 30}
	for in, want := range map[string]string{
		"2026-11-20T10:00:00Z":      "2026-11-20T19:30:00Z",
		"2026-11-20T19:30:00Z":      "2026-11-21T19:30:00Z",
		"2026-11-20T23:59:00Z":      "2026-11-21T19:30:00Z",
		"2026-11-21T03:00:00+08:00": "2026-11-20T19:30:00Z",
	} {
		ti, _ := time.Parse(time.RFC3339, in)
		if got := d.Next(ti).Format(time.RFC3339); got != want {
			t.Errorf("Next(%s) = %s, want %s", in, got, want)
		}
	}
}
