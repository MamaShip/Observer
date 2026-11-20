package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/MamaShip/Observer/internal/backup"
	"github.com/MamaShip/Observer/internal/config"
)

func adminBackup(ctx context.Context, cfg config.Config, cmd string, args []string) error {
	store, err := cfg.Blob()
	if err != nil {
		return err
	}
	svc := backup.New(backup.Config{DatabaseURL: cfg.DatabaseURL}, store, nil, slog.New(slog.DiscardHandler))
	if cmd == "backup" {
		r, err := svc.Run(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("%s  %d bytes  %s\n", r.Key, r.Size, r.Duration.Round(time.Millisecond))
		return nil
	}

	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	target := fs.String("db", "", "empty database to restore into (postgres:// URL)")
	date := fs.String("date", time.Now().UTC().Format("2006-01-02"), "day of the backup (UTC)")
	fs.Parse(args)
	day, err := time.Parse("2006-01-02", *date)
	if err != nil {
		return fmt.Errorf("-date: %w", err)
	}
	if *target == "" {
		return errors.New("usage: observer admin restore -db postgres://... [-date YYYY-MM-DD]")
	}
	if err := ensureEmpty(ctx, *target); err != nil {
		return err
	}
	key := backup.Key(day)
	fmt.Fprintf(os.Stderr, "restoring %s ...\n", key)
	if err := svc.Restore(ctx, key, *target, os.Stderr); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "done; point DATABASE_URL at it, or check it with observer admin stats")
	return nil
}

// ensureEmpty refuses a target that already has tables: restore is for a
// fresh database, never for writing over one in use.
func ensureEmpty(ctx context.Context, url string) error {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return fmt.Errorf("target database: %w", err)
	}
	defer conn.Close(ctx)
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog', 'information_schema')`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("target database has %d tables; restore only into an empty database", n)
	}
	return nil
}
