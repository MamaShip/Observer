// Package store owns the Postgres connection pool and the schema migrations.
// Queries live in queries/*.sql; sqlc generates the Go code in store/db.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// RiverSchemaVersion is the River migration the schema is pinned to. When a
// River upgrade ships a newer migration, TestRiverSchemaPinned fails: add a
// goose migration that calls riverMigration with the new version.
const RiverSchemaVersion = 8

// Open connects to Postgres and verifies the connection.
func Open(ctx context.Context, url string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	return pool, nil
}

// Migrations returns a goose provider over the embedded SQL migrations plus
// the Go migration that creates River's tables. A Postgres advisory lock
// keeps concurrent starts from migrating at the same time. Call closeDB when done.
func Migrations(pool *pgxpool.Pool) (p *goose.Provider, closeDB func(), err error) {
	sqlFiles, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, nil, err
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, nil, err
	}
	db := stdlib.OpenDBFromPool(pool)
	p, err = goose.NewProvider(goose.DialectPostgres, db, sqlFiles,
		goose.WithSessionLocker(locker),
		goose.WithGoMigrations(
			goose.NewGoMigration(2,
				&goose.GoFunc{RunDB: riverMigration(pool, rivermigrate.DirectionUp, RiverSchemaVersion)},
				&goose.GoFunc{RunDB: riverMigration(pool, rivermigrate.DirectionDown, -1)},
			),
		),
	)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return p, func() { _ = db.Close() }, nil
}

// riverMigration runs River's own migrations, so River's tables follow the
// same ordered history as ours. It runs outside a goose transaction because
// some River migrations cannot share one; River records each step in its
// river_migration table, so a retry after a partial failure resumes safely.
func riverMigration(pool *pgxpool.Pool, dir rivermigrate.Direction, target int) func(context.Context, *sql.DB) error {
	return func(ctx context.Context, _ *sql.DB) error {
		m, err := rivermigrate.New(riverpgxv5.New(pool), nil)
		if err != nil {
			return err
		}
		_, err = m.Migrate(ctx, dir, &rivermigrate.MigrateOpts{TargetVersion: target})
		return err
	}
}

// Migrate applies all pending migrations.
func Migrate(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	p, closeDB, err := Migrations(pool)
	if err != nil {
		return err
	}
	defer closeDB()
	results, err := p.Up(ctx)
	for _, r := range results {
		log.Info("migration applied", "version", r.Source.Version, "path", r.Source.Path, "duration", r.Duration)
	}
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
