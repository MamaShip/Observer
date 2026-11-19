package auth

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MamaShip/Observer/internal/store/db"
	"github.com/MamaShip/Observer/internal/store/storetest"
)

type sentLink struct{ to, link string }

type fakeSender struct {
	mu    sync.Mutex
	links []sentLink
}

func (f *fakeSender) SendLogin(_ context.Context, to, link string, _ *uuid.UUID, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.links = append(f.links, sentLink{to, link})
	return nil
}

func (f *fakeSender) last(t *testing.T) (string, string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.links) == 0 {
		t.Fatal("no login email sent")
	}
	l := f.links[len(f.links)-1]
	_, token, ok := strings.Cut(l.link, "#token=")
	if !ok || !strings.HasPrefix(l.link, "https://observer.example/login/verify#token=") {
		t.Fatalf("link = %s", l.link)
	}
	return l.to, token
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.links)
}

func newService(t *testing.T, mode string) (*Service, *fakeSender, *db.Queries) {
	pool, _ := storetest.NewPool(t)
	sender := &fakeSender{}
	return New(pool, sender, Config{BaseURL: "https://observer.example", Mode: mode}, storetest.Logger()), sender, db.New(pool)
}

var ip = netip.MustParseAddr("203.0.113.7")

func TestNormalizeEmail(t *testing.T) {
	for in, want := range map[string]string{
		"User@Example.COM": "user@example.com", " a.b+c@example.org ": "a.b+c@example.org",
		"x@localhost": "", "Name <a@example.com>": "", "not-an-email": "", "a@b@example.com": "",
	} {
		got, err := NormalizeEmail(in)
		if want == "" {
			if !errors.Is(err, ErrInvalidEmail) {
				t.Errorf("NormalizeEmail(%q) = %q, %v; want ErrInvalidEmail", in, got, err)
			}
		} else if got != want || err != nil {
			t.Errorf("NormalizeEmail(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestOpenModeLoginFlow(t *testing.T) {
	ctx := context.Background()
	s, sender, q := newService(t, ModeOpen)

	if err := s.RequestLink(ctx, "New.User@Example.com", ip); err != nil {
		t.Fatal(err)
	}
	to, token := sender.last(t)
	if to != "new.user@example.com" {
		t.Errorf("sent to %s", to)
	}
	sess, err := s.Verify(ctx, token, "test-agent")
	if err != nil {
		t.Fatal(err)
	}
	if sess.User.Email != "new.user@example.com" || sess.Token == "" || sess.Token == token {
		t.Fatalf("session = %+v", sess)
	}
	if _, err := s.Verify(ctx, token, "again"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("reused token: %v, want ErrInvalidToken", err)
	}

	u, err := s.Authenticate(ctx, sess.Token)
	if err != nil || u.ID != sess.User.ID {
		t.Fatalf("Authenticate = %v, %v", u.ID, err)
	}
	if _, err := s.Authenticate(ctx, "garbage"); !errors.Is(err, ErrNoSession) {
		t.Errorf("garbage token: %v", err)
	}

	// Disabled accounts lose their sessions' access and get no links.
	if _, err := q.SetUserStatus(ctx, db.SetUserStatusParams{Email: u.Email, Status: "disabled"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, sess.Token); !errors.Is(err, ErrDisabled) {
		t.Errorf("disabled user authenticated: %v", err)
	}
	n := sender.count()
	if err := s.RequestLink(ctx, u.Email, ip); err != nil || sender.count() != n {
		t.Errorf("disabled user got a link (err %v)", err)
	}

	if err := s.Logout(ctx, sess.Token); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredToken(t *testing.T) {
	ctx := context.Background()
	s, sender, _ := newService(t, ModeOpen)
	if err := s.RequestLink(ctx, "a@example.com", ip); err != nil {
		t.Fatal(err)
	}
	_, token := sender.last(t)
	s.now = func() time.Time { return time.Now().Add(LoginTokenTTL + time.Minute) }
	if _, err := s.Verify(ctx, token, ""); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired token: %v", err)
	}
}

func TestInviteMode(t *testing.T) {
	ctx := context.Background()
	s, sender, q := newService(t, ModeInvite)

	// Unknown address: same answer, no email.
	if err := s.RequestLink(ctx, "stranger@example.com", ip); err != nil || sender.count() != 0 {
		t.Fatalf("uninvited: err=%v sent=%d", err, sender.count())
	}
	if _, err := q.CreateUser(ctx, "invited@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := s.RequestLink(ctx, "Invited@example.com", ip); err != nil {
		t.Fatal(err)
	}
	_, token := sender.last(t)
	if _, err := s.Verify(ctx, token, ""); err != nil {
		t.Fatal(err)
	}
}

func TestRateLimits(t *testing.T) {
	ctx := context.Background()
	s, sender, _ := newService(t, ModeOpen)

	// Per address: the 4th link within 10 minutes is silently dropped.
	for range 4 {
		if err := s.RequestLink(ctx, "a@example.com", ip); err != nil {
			t.Fatal(err)
		}
	}
	if sender.count() != linksPerEmail {
		t.Errorf("sent %d links, want %d", sender.count(), linksPerEmail)
	}
	// Per IP: an error after 10 requests in an hour.
	var err error
	for i := range 10 {
		err = s.RequestLink(ctx, "user"+string(rune('a'+i))+"@example.com", ip)
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("11th request from one IP: %v, want ErrRateLimited", err)
	}
	if err := s.RequestLink(ctx, "other@example.com", netip.MustParseAddr("198.51.100.1")); err != nil {
		t.Errorf("another IP was limited: %v", err)
	}
}

func TestSessionSlidingExpiry(t *testing.T) {
	ctx := context.Background()
	s, sender, _ := newService(t, ModeOpen)
	if err := s.RequestLink(ctx, "a@example.com", ip); err != nil {
		t.Fatal(err)
	}
	_, token := sender.last(t)
	sess, err := s.Verify(ctx, token, "")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	// Used every 20 days, the session outlives its initial 30 days.
	for _, day := range []int{20, 40, 60} {
		s.now = func() time.Time { return start.Add(time.Duration(day) * 24 * time.Hour) }
		if _, err := s.Authenticate(ctx, sess.Token); err != nil {
			t.Fatalf("day %d: %v", day, err)
		}
	}
	s.now = func() time.Time { return start.Add(91 * 24 * time.Hour) }
	if _, err := s.Authenticate(ctx, sess.Token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("idle 31 days: %v, want ErrNoSession", err)
	}
	if err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
}
