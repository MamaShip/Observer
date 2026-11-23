package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/MamaShip/Observer/internal/store"
	"github.com/MamaShip/Observer/internal/store/db"
	"github.com/MamaShip/Observer/internal/store/storetest"
)

// A River upgrade that ships a new migration must come with a goose
// migration that applies it (see store.RiverSchemaVersion).
func TestRiverSchemaPinned(t *testing.T) {
	m, err := rivermigrate.New(riverpgxv5.New(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	all := m.AllVersions()
	if latest := all[len(all)-1].Version; latest != store.RiverSchemaVersion {
		t.Fatalf("River ships migration %d but the schema is pinned to %d: add a goose migration", latest, store.RiverSchemaVersion)
	}
}

func TestMigrateRoundTrip(t *testing.T) {
	ctx := context.Background()
	url := storetest.EmptyDB(t)
	pool, err := store.Open(ctx, url, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	log := storetest.Logger()

	exists := func(table string) bool {
		var ok bool
		if err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}

	if err := store.Migrate(ctx, pool, log); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"users", "articles", "watches", "check_runs", "email_log", "river_job", "river_migration"} {
		if !exists(table) {
			t.Errorf("table %s missing after migrate", table)
		}
	}
	// Second run is a no-op.
	if err := store.Migrate(ctx, pool, log); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	// Down to zero removes everything, and the schema can be rebuilt.
	p, closeDB, err := store.Migrations(pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("down: %v", err)
	}
	closeDB()
	for _, table := range []string{"users", "river_job", "river_migration"} {
		if exists(table) {
			t.Errorf("table %s still present after down", table)
		}
	}
	if err := store.Migrate(ctx, pool, log); err != nil {
		t.Fatalf("migrate after down: %v", err)
	}
}

// Links made before 00014 could have up to 1000 places; upgrading cuts them
// down to the new cap instead of failing.
func TestMigrateOldInviteLinkCap(t *testing.T) {
	ctx := context.Background()
	pool, err := store.Open(ctx, storetest.EmptyDB(t), 5)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	p, closeDB, err := store.Migrations(pool)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	if _, err := p.UpTo(ctx, 13); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO invite_links (max_uses, expires_at, revoked_at) VALUES (101, now(), now()), (20, now(), NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 14); err != nil {
		t.Fatalf("migrate with an old link: %v", err)
	}
	var caps []int32
	rows, _ := pool.Query(ctx, `SELECT max_uses FROM invite_links ORDER BY max_uses`)
	for rows.Next() {
		var c int32
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		caps = append(caps, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(caps) != 2 || caps[0] != 20 || caps[1] != 100 {
		t.Errorf("max_uses after migrating = %v, want [20 100]", caps)
	}
}

func TestSchemaDefaultsAndStats(t *testing.T) {
	ctx := context.Background()
	pool, _ := storetest.NewPool(t)
	q := db.New(pool)

	var name string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(name, "observer_test_") {
		t.Fatalf("test is running against database %q, not its own", name)
	}

	s, err := q.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s != (db.StatsRow{}) {
		t.Fatalf("stats on empty database = %+v", s)
	}

	// Primary keys default to UUIDv7 (Postgres 18) and email is case-insensitive.
	var id uuid.UUID
	if err := pool.QueryRow(ctx, "INSERT INTO users (email) VALUES ('A@Example.com') RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id.Version() != 7 {
		t.Errorf("user id %s is UUID version %d, want 7", id, id.Version())
	}
	if _, err := pool.Exec(ctx, "INSERT INTO users (email) VALUES ('a@example.COM')"); err == nil {
		t.Error("duplicate email in different case was accepted")
	}

	if _, err := pool.Exec(ctx, `INSERT INTO articles (status) VALUES ('alive'), ('deleted')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO articles (status) VALUES ('gone')`); err == nil {
		t.Error("invalid article status was accepted")
	}
	s, err = q.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.Users != 1 || s.Articles != 2 || s.DeletedArticles != 1 {
		t.Fatalf("stats = %+v", s)
	}
}
