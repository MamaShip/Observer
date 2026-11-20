package watch

import (
	"errors"
	"sync"
	"testing"
)

const otherURL = "https://mp.weixin.qq.com/s/aIWmD5E2y-Yg7oMltd2i8w"

func TestSubmitQuota(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@example.com")
	e.web.set(shortURL, fixture(t, "alive_article.html"))
	quota := Quota{MaxOpen: 1, MaxDaily: 2}

	w, err := e.svc.Submit(e.ctx, alice, shortURL, quota)
	if err != nil {
		t.Fatal(err)
	}
	// At the limit, the same link again returns her watch.
	if again, err := e.svc.Submit(e.ctx, alice, shortURL+"?scene=1", quota); err != nil || again.ID != w.ID {
		t.Errorf("same link at the limit = %v, %v", again.ID, err)
	}
	// Another link is refused and leaves nothing behind.
	if _, err := e.svc.Submit(e.ctx, alice, otherURL, quota); !errors.Is(err, ErrOpenLimit) {
		t.Errorf("second article: %v, want ErrOpenLimit", err)
	}
	if n := e.count("SELECT count(*) FROM articles"); n != 1 {
		t.Errorf("%d articles after a refused submission, want 1", n)
	}
	if jobs := take[ResolveArgs](e.jobs); len(jobs) != 1 {
		t.Errorf("%d resolve jobs queued, want 1", len(jobs))
	}

	// Without an open limit, the day's submissions still count.
	if _, err := e.svc.Submit(e.ctx, alice, otherURL, Quota{MaxDaily: 1}); !errors.Is(err, ErrDailyLimit) {
		t.Errorf("over the daily limit: %v, want ErrDailyLimit", err)
	}
	if _, err := e.svc.Submit(e.ctx, alice, otherURL, Quota{MaxDaily: 2}); err != nil {
		t.Errorf("within the daily limit: %v", err)
	}
}

// Concurrent submissions by one user are counted one after the other.
func TestSubmitQuotaConcurrent(t *testing.T) {
	e := newEnv(t)
	bob := e.user("bob@example.com")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, u := range []string{shortURL, otherURL} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = e.svc.Submit(e.ctx, bob, u, Quota{MaxOpen: 1})
		}()
	}
	wg.Wait()
	ok, limited := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrOpenLimit):
			limited++
		default:
			t.Fatal(err)
		}
	}
	if ok != 1 || limited != 1 {
		t.Errorf("%d accepted, %d refused; want 1 and 1", ok, limited)
	}
	if n := e.count("SELECT count(*) FROM watches WHERE user_id = $1", bob); n != 1 {
		t.Errorf("bob has %d watches, want 1", n)
	}
}

func TestRenewQuota(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@example.com")
	w, _ := e.aliveArticle(alice)
	if _, err := e.pool.Exec(e.ctx, "UPDATE watches SET status = 'ended', end_reason = 'user_stopped', ended_at = now() WHERE id = $1", w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Submit(e.ctx, alice, otherURL, Quota{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Renew(e.ctx, alice, w.ID, Quota{MaxOpen: 1}); !errors.Is(err, ErrOpenLimit) {
		t.Errorf("reopening at the limit: %v, want ErrOpenLimit", err)
	}
	if _, err := e.svc.Renew(e.ctx, alice, w.ID, Quota{MaxOpen: 2}); err != nil {
		t.Errorf("reopening below the limit: %v", err)
	}
	// Extending an open watch does not count.
	if _, err := e.svc.Renew(e.ctx, alice, w.ID, Quota{MaxOpen: 1}); err != nil {
		t.Errorf("extending at the limit: %v", err)
	}
}
