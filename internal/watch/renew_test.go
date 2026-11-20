package watch

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestRenewable(t *testing.T) {
	for _, tt := range []struct {
		status, reason, article string
		want                    bool
	}{
		{"active", "", "alive", true},
		{"ended", "expired", "alive", true},
		{"ended", "expired", "dormant", true},
		{"ended", "user_stopped", "alive", true},
		{"ended", "expired", "deleted", false},
		{"ended", "deleted", "deleted", false},
		{"ended", "user_stopped", "invalid", false},
		{"resolving", "", "pending", false},
		{"failed", "invalid", "invalid", false},
		{"failed", "unreachable", "pending", false},
	} {
		if got := Renewable(tt.status, tt.reason, tt.article); got != tt.want {
			t.Errorf("Renewable(%s, %s, %s) = %v", tt.status, tt.reason, tt.article, got)
		}
	}
}

func TestRenew(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@example.com")
	bob := e.user("bob@example.com")
	w, art := e.aliveArticle(alice)
	start := time.Now()
	e.svc.now = func() time.Time { return start.Add(50 * 24 * time.Hour) }

	// Not hers.
	if _, err := e.svc.Renew(e.ctx, bob, w.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("someone else's watch: %v", err)
	}
	// An active watch runs to 60 days from now.
	got, err := e.svc.Renew(e.ctx, alice, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := start.Add(110 * 24 * time.Hour); got.Status != "active" || got.ExpiresAt.Sub(want).Abs() > time.Second {
		t.Errorf("extended = %s until %v, want %v", got.Status, got.ExpiresAt, want)
	}

	// Expired, the article left dormant: watched again and checked at once.
	e.svc.now = time.Now
	if _, err := e.pool.Exec(e.ctx, "UPDATE watches SET expires_at = now() - interval '1 second' WHERE id = $1", w.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Expire(e.ctx); err != nil {
		t.Fatal(err)
	}
	if a := e.article(art.ID); a.Status != "dormant" {
		t.Fatalf("article = %s, want dormant", a.Status)
	}
	got, err = e.svc.Renew(e.ctx, alice, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "active" || got.EndReason != nil || got.EndedAt != nil || got.NotifiedAt != nil ||
		got.ExpiresAt.Before(time.Now().Add(59*24*time.Hour)) {
		t.Errorf("reopened = %+v", got)
	}
	if a := e.article(art.ID); a.Status != "alive" || a.NextCheckAt == nil || a.NextCheckAt.After(time.Now()) {
		t.Errorf("article = %s next %v, want alive and due now", a.Status, a.NextCheckAt)
	}
	// The next Expire leaves it alone.
	if err := e.svc.Expire(e.ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.watch(w.ID); got.Status != "active" {
		t.Errorf("after Expire: %s", got.Status)
	}

	// Stopped, then watched again.
	if _, err := e.pool.Exec(e.ctx, "UPDATE watches SET status = 'ended', end_reason = 'user_stopped', ended_at = now(), notified_at = now() WHERE id = $1", w.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := e.svc.Renew(e.ctx, alice, w.ID); err != nil || got.Status != "active" {
		t.Errorf("stopped: %+v, %v", got, err)
	}

	// Stopped, and meanwhile submitted again: the new watch is the one.
	if _, err := e.pool.Exec(e.ctx, "UPDATE watches SET status = 'ended', end_reason = 'user_stopped', ended_at = now() WHERE id = $1", w.ID); err != nil {
		t.Fatal(err)
	}
	w2 := e.submit(alice, shortURL)
	if _, err := e.svc.Renew(e.ctx, alice, w.ID); !errors.Is(err, ErrWatchingAgain) {
		t.Errorf("with another open watch: %v", err)
	}
	if _, err := e.svc.Renew(e.ctx, alice, w2.ID); err != nil {
		t.Errorf("the new watch: %v", err)
	}

	// Deleted articles cannot be watched again.
	if _, err := e.pool.Exec(e.ctx, "UPDATE articles SET status = 'deleted' WHERE id = $1", art.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Renew(e.ctx, alice, w.ID); !errors.Is(err, ErrNotRenewable) {
		t.Errorf("deleted article: %v", err)
	}
}
