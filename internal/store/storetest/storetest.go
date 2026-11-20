// Package storetest gives tests a fresh, migrated Postgres database.
//
// Tests that use it are skipped unless TEST_DATABASE_URL points at a server
// where that role may create databases (`make test` starts one in Docker).
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MamaShip/Observer/internal/store"
)

// URL returns TEST_DATABASE_URL, skipping the test when it is not set.
func URL(t testing.TB) string {
	t.Helper()
	u := os.Getenv("TEST_DATABASE_URL")
	if u == "" {
		t.Skip("TEST_DATABASE_URL not set; run `make test` to include database tests")
	}
	return u
}

// EmptyDB creates an empty database for this test, dropped on cleanup, and
// returns its connection URL. Tests in different packages run in parallel,
// so each one must use its own database.
func EmptyDB(t testing.TB) string {
	t.Helper()
	base := URL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	defer admin.Close(ctx)

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "observer_test_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c, err := pgx.Connect(ctx, base)
		if err != nil {
			t.Errorf("drop test database: %v", err)
			return
		}
		defer c.Close(ctx)
		if _, err := c.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop test database: %v", err)
		}
	})

	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" {
		t.Fatalf("TEST_DATABASE_URL must be a postgres:// URL: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

// NewPool returns a pool on a fresh, fully migrated database.
func NewPool(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	dbURL := EmptyDB(t)
	ctx := context.Background()
	pool, err := store.Open(ctx, dbURL, 5)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := store.Migrate(ctx, pool, Logger()); err != nil {
		t.Fatal(err)
	}
	return pool, dbURL
}

// Logger discards output; set TEST_LOG=1 to see it.
func Logger() *slog.Logger {
	if os.Getenv("TEST_LOG") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
