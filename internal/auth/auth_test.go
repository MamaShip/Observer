package auth

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/MamaShip/Observer/internal/store/db"
	"github.com/MamaShip/Observer/internal/store/storetest"
)

type sentCode struct {
	to, code string
	join     bool // through an invitation link
}

type fakeSender struct {
	mu          sync.Mutex
	codes       []sentCode
	inviteToken string
	fail        error
	joinFail    error // only for codes through invitation links
}

func (f *fakeSender) SendLogin(_ context.Context, to, code string, _ *uuid.UUID, valid time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	if valid != CodeTTL {
		panic("unexpected validity")
	}
	f.codes = append(f.codes, sentCode{to: to, code: code})
	return nil
}

func (f *fakeSender) SendJoinCode(_ context.Context, to, code string, _ *uuid.UUID, valid time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.joinFail != nil {
		return f.joinFail
	}
	if f.fail != nil {
		return f.fail
	}
	if valid != CodeTTL {
		panic("unexpected validity")
	}
	f.codes = append(f.codes, sentCode{to: to, code: code, join: true})
	return nil
}

func (f *fakeSender) SendInvite(_ context.Context, _, token string, valid time.Duration) error {
	if valid != InviteLinkTTL {
		panic("unexpected invite validity")
	}
	if f.fail == nil {
		f.inviteToken = token
	}
	return f.fail
}

func (f *fakeSender) last(t *testing.T) (string, string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.codes) == 0 {
		t.Fatal("no login email sent")
	}
	c := f.codes[len(f.codes)-1]
	if !validCode(c.code) {
		t.Fatalf("code = %q", c.code)
	}
	return c.to, c.code
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.codes)
}

// clock is a settable time source for the service.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newService(t *testing.T, mode string) (*Service, *fakeSender, *db.Queries, *clock) {
	pool, _ := storetest.NewPool(t)
	sender := &fakeSender{}
	s := New(pool, sender, Config{Mode: mode}, storetest.Logger())
	jobs, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	s.SetJobs(jobs)
	// 10:00 in Beijing.
	c := &clock{t: time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC)}
	s.now = c.now
	return s, sender, db.New(pool), c
}

var ip = netip.MustParseAddr("203.0.113.7")

// wrong returns a valid-looking code different from code.
func wrong(code string) string {
	if code == "000000" {
		return "000001"
	}
	return "000000"
}

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
	s, sender, q, _ := newService(t, ModeOpen)

	quota, err := s.RequestCode(ctx, "New.User@Example.com", "", ip)
	if err != nil {
		t.Fatal(err)
	}
	if quota != (CodeQuota{Remaining: 1, ResendIn: ResendAfter}) {
		t.Errorf("quota = %+v", quota)
	}
	to, code := sender.last(t)
	if to != "new.user@example.com" {
		t.Errorf("sent to %s", to)
	}
	if _, err := s.Verify(ctx, "new.user@example.com", wrong(code), "", ""); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("wrong code: %v", err)
	}
	if _, err := s.Verify(ctx, "other@example.com", code, "", ""); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("code for another address: %v", err)
	}
	sess, err := s.Verify(ctx, "NEW.user@example.com", " "+code+" ", "", "test-agent")
	if err != nil {
		t.Fatal(err)
	}
	if sess.User.Email != "new.user@example.com" || sess.Token == "" {
		t.Fatalf("session = %+v", sess)
	}
	if _, err := s.Verify(ctx, "new.user@example.com", code, "", "again"); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("reused code: %v, want ErrInvalidCode", err)
	}

	u, err := s.Authenticate(ctx, sess.Token)
	if err != nil || u.ID != sess.User.ID {
		t.Fatalf("Authenticate = %v, %v", u.ID, err)
	}
	if _, err := s.Authenticate(ctx, "garbage"); !errors.Is(err, ErrNoSession) {
		t.Errorf("garbage token: %v", err)
	}

	// Disabled accounts lose their sessions' access and get no codes.
	if _, err := q.SetUserStatus(ctx, db.SetUserStatusParams{Email: u.Email, Status: "disabled"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, sess.Token); !errors.Is(err, ErrDisabled) {
		t.Errorf("disabled user authenticated: %v", err)
	}
	if err := s.Logout(ctx, sess.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateUser(ctx, "d@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.SetUserStatus(ctx, db.SetUserStatusParams{Email: "d@example.com", Status: "disabled"}); err != nil {
		t.Fatal(err)
	}
	n := sender.count()
	if _, err := s.RequestCode(ctx, "d@example.com", "", ip); err != nil || sender.count() != n {
		t.Errorf("disabled user got a code (err %v)", err)
	}
}

func TestCodeExpiresAndRunsOutOfGuesses(t *testing.T) {
	ctx := context.Background()
	s, sender, _, clk := newService(t, ModeOpen)

	if _, err := s.RequestCode(ctx, "a@example.com", "", ip); err != nil {
		t.Fatal(err)
	}
	_, code := sender.last(t)
	clk.advance(CodeTTL + time.Second)
	if _, err := s.Verify(ctx, "a@example.com", code, "", ""); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("expired code: %v", err)
	}

	if _, err := s.RequestCode(ctx, "a@example.com", "", ip); err != nil {
		t.Fatal(err)
	}
	_, code = sender.last(t)
	for range codeAttempts {
		if _, err := s.Verify(ctx, "a@example.com", wrong(code), "", ""); !errors.Is(err, ErrInvalidCode) {
			t.Fatal(err)
		}
	}
	if _, err := s.Verify(ctx, "a@example.com", code, "", ""); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("right code after %d wrong guesses: %v, want ErrInvalidCode", codeAttempts, err)
	}
	// Malformed input does not use up guesses.
	for _, c := range []string{"", "12345", "1234567", "12345a"} {
		if _, err := s.Verify(ctx, "a@example.com", c, "", ""); !errors.Is(err, ErrInvalidCode) {
			t.Errorf("Verify(%q) = %v", c, err)
		}
	}
}

func TestOnlyLatestCodeWorks(t *testing.T) {
	ctx := context.Background()
	s, sender, _, clk := newService(t, ModeOpen)
	if _, err := s.RequestCode(ctx, "a@example.com", "", ip); err != nil {
		t.Fatal(err)
	}
	_, first := sender.last(t)
	clk.advance(ResendAfter)
	if _, err := s.RequestCode(ctx, "a@example.com", "", ip); err != nil {
		t.Fatal(err)
	}
	_, second := sender.last(t)
	if first != second {
		if _, err := s.Verify(ctx, "a@example.com", first, "", ""); !errors.Is(err, ErrInvalidCode) {
			t.Errorf("superseded code: %v", err)
		}
	}
	if _, err := s.Verify(ctx, "a@example.com", second, "", ""); err != nil {
		t.Fatal(err)
	}
}

func TestInviteMode(t *testing.T) {
	ctx := context.Background()
	s, sender, q, clk := newService(t, ModeInvite)

	// Unknown address: same answers as a real one, no email.
	stranger := make([]error, 3)
	for i := range stranger {
		_, stranger[i] = s.RequestCode(ctx, "stranger@example.com", "", ip)
		clk.advance(ResendAfter)
	}
	if sender.count() != 0 {
		t.Fatalf("uninvited address got %d emails", sender.count())
	}
	if _, err := q.CreateUser(ctx, "invited@example.com"); err != nil {
		t.Fatal(err)
	}
	invited := make([]error, 3)
	for i := range invited {
		_, invited[i] = s.RequestCode(ctx, "Invited@example.com", "", ip)
		clk.advance(ResendAfter)
	}
	for i := range invited {
		if !errors.Is(stranger[i], invited[i]) {
			t.Errorf("request %d: stranger %v, invited %v", i+1, stranger[i], invited[i])
		}
	}
	if sender.count() != CodesPerDay {
		t.Fatalf("invited address got %d emails", sender.count())
	}
	_, code := sender.last(t)
	if _, err := s.Verify(ctx, "invited@example.com", code, "", ""); err != nil {
		t.Fatal(err)
	}
}

func TestDailyLimit(t *testing.T) {
	ctx := context.Background()
	s, sender, _, clk := newService(t, ModeOpen)

	if _, err := s.RequestCode(ctx, "a@example.com", "", ip); err != nil {
		t.Fatal(err)
	}
	// A second request within a minute is refused and not counted.
	clk.advance(20 * time.Second)
	q, err := s.RequestCode(ctx, "a@example.com", "", ip)
	if !errors.Is(err, ErrTooSoon) || q.ResendIn != ResendAfter-20*time.Second || q.Remaining != 1 {
		t.Fatalf("too soon: %+v, %v", q, err)
	}
	clk.advance(40 * time.Second)
	q, err = s.RequestCode(ctx, "a@example.com", "", ip)
	if err != nil || q != (CodeQuota{}) {
		t.Fatalf("second code: %+v, %v", q, err)
	}
	clk.advance(time.Hour)
	if _, err := s.RequestCode(ctx, "a@example.com", "", ip); !errors.Is(err, ErrTryTomorrow) {
		t.Fatalf("third code: %v, want ErrTryTomorrow", err)
	}
	if sender.count() != CodesPerDay {
		t.Fatalf("sent %d codes", sender.count())
	}

	// A failed send does not count.
	sender.fail = errors.New("smtp down")
	if _, err := s.RequestCode(ctx, "b@example.com", "", ip); err == nil {
		t.Fatal("send failure not reported")
	}
	sender.fail = nil
	clk.advance(ResendAfter)
	if q, err := s.RequestCode(ctx, "b@example.com", "", ip); err != nil || q.Remaining != 1 {
		t.Fatalf("after a failed send: %+v, %v", q, err)
	}

	// The day turns at midnight Beijing time (16:00 UTC).
	clk.t = time.Date(2026, 11, 20, 15, 59, 0, 0, time.UTC)
	if _, err := s.RequestCode(ctx, "a@example.com", "", ip); !errors.Is(err, ErrTryTomorrow) {
		t.Fatalf("23:59 Beijing: %v", err)
	}
	clk.t = time.Date(2026, 11, 20, 16, 0, 0, 0, time.UTC)
	if _, err := s.RequestCode(ctx, "a@example.com", "", ip); err != nil {
		t.Fatalf("00:00 Beijing: %v", err)
	}
}

func TestIPRateLimit(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := newService(t, ModeOpen)
	var err error
	for i := range codesPerIPPerHour + 1 {
		_, err = s.RequestCode(ctx, "user"+string(rune('a'+i))+"@example.com", "", ip)
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("request %d from one IP: %v, want ErrRateLimited", codesPerIPPerHour+1, err)
	}
	if _, err := s.RequestCode(ctx, "other@example.com", "", netip.MustParseAddr("198.51.100.1")); err != nil {
		t.Errorf("another IP was limited: %v", err)
	}
}

func TestSessionSlidingExpiry(t *testing.T) {
	ctx := context.Background()
	s, sender, _, clk := newService(t, ModeOpen)
	if _, err := s.RequestCode(ctx, "a@example.com", "", ip); err != nil {
		t.Fatal(err)
	}
	_, code := sender.last(t)
	sess, err := s.Verify(ctx, "a@example.com", code, "", "")
	if err != nil {
		t.Fatal(err)
	}
	start := clk.t
	if _, renewed, err := s.Resume(ctx, sess.Token); err != nil || renewed {
		t.Fatalf("right after login: renewed %v, %v; want not renewed", renewed, err)
	}
	// Used every 40 days, the session outlives its initial 60 days.
	for _, day := range []int{40, 80, 120} {
		clk.t = start.Add(time.Duration(day) * 24 * time.Hour)
		if _, renewed, err := s.Resume(ctx, sess.Token); err != nil || !renewed {
			t.Fatalf("day %d: renewed %v, %v; want renewed", day, renewed, err)
		}
		// Extended at most once an hour.
		if _, renewed, err := s.Resume(ctx, sess.Token); err != nil || renewed {
			t.Fatalf("day %d again: renewed %v, %v; want not renewed", day, renewed, err)
		}
	}
	clk.t = start.Add(181 * 24 * time.Hour)
	if _, err := s.Authenticate(ctx, sess.Token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("idle 61 days: %v, want ErrNoSession", err)
	}
	if err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	var codes, reqs, sessions int
	if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM login_codes), (SELECT count(*) FROM login_requests), (SELECT count(*) FROM sessions)`).Scan(&codes, &reqs, &sessions); err != nil {
		t.Fatal(err)
	}
	if codes+reqs+sessions != 0 {
		t.Errorf("after cleanup: %d codes, %d request counts, %d sessions", codes, reqs, sessions)
	}
}

func TestDeleteAccount(t *testing.T) {
	ctx := context.Background()
	s, sender, q, _ := newService(t, ModeOpen)
	if _, err := s.RequestCode(ctx, "gone@example.com", "", ip); err != nil {
		t.Fatal(err)
	}
	_, code := sender.last(t)
	sess, err := s.Verify(ctx, "gone@example.com", code, "", "")
	if err != nil {
		t.Fatal(err)
	}
	// One email logged before the account existed, one after.
	for _, uid := range []*uuid.UUID{nil, &sess.User.ID} {
		if _, err := q.InsertEmailLog(ctx, db.InsertEmailLogParams{UserID: uid, Kind: "login", ToEmail: "gone@example.com", Subject: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.InsertEmailLog(ctx, db.InsertEmailLogParams{Kind: "login", ToEmail: "other@example.com", Subject: "x"}); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteAccount(ctx, sess.User.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, sess.User.ID); err != nil {
		t.Errorf("deleting again: %v", err)
	}
	var users, codes, logged, scrubbed, others, requests int
	if err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM users WHERE email = 'gone@example.com'),
		(SELECT count(*) FROM login_codes WHERE email = 'gone@example.com'),
		(SELECT count(*) FROM email_log WHERE to_email = 'gone@example.com'),
		(SELECT count(*) FROM email_log WHERE to_email = 'deleted' AND user_id IS NULL),
		(SELECT count(*) FROM email_log WHERE to_email = 'other@example.com'),
		(SELECT count(*) FROM login_requests WHERE email = 'gone@example.com')`).Scan(&users, &codes, &logged, &scrubbed, &others, &requests); err != nil {
		t.Fatal(err)
	}
	if users != 0 || codes != 0 || logged != 0 || scrubbed != 2 || others != 1 {
		t.Errorf("after delete: users=%d codes=%d logged=%d scrubbed=%d others=%d", users, codes, logged, scrubbed, others)
	}
	if requests != 1 {
		t.Errorf("the day's code count should stay (abuse limit), got %d rows", requests)
	}
	if _, err := s.Authenticate(ctx, sess.Token); !errors.Is(err, ErrNoSession) {
		t.Errorf("session of a deleted account: %v", err)
	}
}

func TestCleanupEmailLog(t *testing.T) {
	ctx := context.Background()
	s, _, q, clk := newService(t, ModeOpen)
	if _, err := q.InsertEmailLog(ctx, db.InsertEmailLogParams{Kind: "login", ToEmail: "a@example.com", Subject: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE email_log SET created_at = $1`, clk.t.Add(-EmailLogRetention-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := q.InsertEmailLog(ctx, db.InsertEmailLogParams{Kind: "login", ToEmail: "b@example.com", Subject: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	var left []string
	rows, _ := s.pool.Query(ctx, `SELECT to_email FROM email_log`)
	for rows.Next() {
		var e string
		_ = rows.Scan(&e)
		left = append(left, e)
	}
	if len(left) != 1 || left[0] != "b@example.com" {
		t.Errorf("email_log after cleanup = %v", left)
	}
}

func TestIssueSession(t *testing.T) {
	ctx := context.Background()
	s, _, q, _ := newService(t, ModeInvite)
	if _, err := s.IssueSession(ctx, "nobody@example.com", ""); err == nil {
		t.Error("session for an unknown address")
	}
	if _, err := q.CreateUser(ctx, "ops@example.com"); err != nil {
		t.Fatal(err)
	}
	sess, err := s.IssueSession(ctx, "OPS@example.com", "smoke")
	if err != nil {
		t.Fatal(err)
	}
	if u, err := s.Authenticate(ctx, sess.Token); err != nil || u.Email != "ops@example.com" {
		t.Fatalf("Authenticate = %v, %v", u.Email, err)
	}
	if _, err := q.SetUserStatus(ctx, db.SetUserStatusParams{Email: "ops@example.com", Status: "disabled"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueSession(ctx, "ops@example.com", ""); !errors.Is(err, ErrDisabled) {
		t.Errorf("disabled account: %v", err)
	}
}

func TestLogoutAll(t *testing.T) {
	ctx := context.Background()
	s, sender, _, clk := newService(t, ModeOpen)
	var tokens []string
	for range 2 {
		if _, err := s.RequestCode(ctx, "a@example.com", "", ip); err != nil {
			t.Fatal(err)
		}
		_, code := sender.last(t)
		sess, err := s.Verify(ctx, "a@example.com", code, "", "")
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, sess.Token)
		clk.advance(ResendAfter)
	}
	u, err := s.Authenticate(ctx, tokens[0])
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.LogoutAll(ctx, u.ID); err != nil || n != 2 {
		t.Fatalf("LogoutAll = %d, %v", n, err)
	}
	for _, tok := range tokens {
		if _, err := s.Authenticate(ctx, tok); !errors.Is(err, ErrNoSession) {
			t.Errorf("session survived LogoutAll: %v", err)
		}
	}
}
