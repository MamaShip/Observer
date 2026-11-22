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

// noIP skips the per-address limiter, which is tested elsewhere.
var noIP netip.Addr

func newLink(t *testing.T, s *Service, maxUses int) (db.InviteLink, string) {
	t.Helper()
	l, err := s.CreateInviteLink(context.Background(), nil, maxUses, 24*time.Hour, "群聊")
	if err != nil {
		t.Fatal(err)
	}
	return l, s.LinkToken(l.ID)
}

// signUp requests a code through the link and logs in with it.
func signUp(t *testing.T, s *Service, sender *fakeSender, email, token string) (Session, error) {
	t.Helper()
	n := sender.count()
	if _, err := s.RequestCode(context.Background(), email, token, noIP); err != nil {
		return Session{}, err
	}
	if sender.count() != n+1 {
		t.Fatalf("%s: no code sent", email)
	}
	_, code := sender.last(t)
	return s.Verify(context.Background(), email, code, token, "")
}

func TestLinkToken(t *testing.T) {
	pool, _ := storetest.NewPool(t)
	key := []byte(strings.Repeat("k", 32))
	s := New(pool, nil, Config{Mode: ModeInvite, LinkKey: key}, storetest.Logger())
	id := uuid.Must(uuid.NewV7())
	tok := s.LinkToken(id)
	if len(tok) != 43 {
		t.Fatalf("token %q", tok)
	}
	if got, ok := s.parseLinkToken(tok); !ok || got != id {
		t.Errorf("parse = %v, %v", got, ok)
	}
	// The same key gives the same link after a restart.
	if again := New(pool, nil, Config{Mode: ModeInvite, LinkKey: key}, storetest.Logger()).LinkToken(id); again != tok {
		t.Errorf("token changed with the same key")
	}
	other := New(pool, nil, Config{Mode: ModeInvite, LinkKey: []byte(strings.Repeat("x", 32))}, storetest.Logger())
	flipped := []byte(tok)
	flipped[42] ^= 1
	for name, bad := range map[string]string{
		"other key": other.LinkToken(id), "tampered": string(flipped), "short": tok[:42], "empty": "",
		"not base64": strings.Repeat("!", 43),
	} {
		if _, ok := s.parseLinkToken(bad); ok {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCreateInviteLink(t *testing.T) {
	ctx := context.Background()
	s, _, _, clk := newService(t, ModeInvite)
	for name, c := range map[string]struct {
		n     int
		valid time.Duration
		note  string
	}{
		"no places": {0, time.Hour, ""}, "too many": {LinkMaxUses + 1, time.Hour, ""},
		"too short": {1, time.Minute, ""}, "too long": {1, LinkMaxValid + time.Hour, ""},
		"long note": {1, time.Hour, strings.Repeat("字", LinkNoteMax+1)},
	} {
		if _, err := s.CreateInviteLink(ctx, nil, c.n, c.valid, c.note); !errors.Is(err, ErrLinkSettings) {
			t.Errorf("%s: %v", name, err)
		}
	}
	l, err := s.CreateInviteLink(ctx, nil, LinkMaxUses, LinkMaxValid, " "+strings.Repeat("字", LinkNoteMax)+" ")
	if err != nil {
		t.Fatal(err)
	}
	if !l.ExpiresAt.Equal(clk.now().Add(LinkMaxValid)) || l.Used != 0 || LinkStatus(l, clk.now()) != LinkActive {
		t.Errorf("link = %+v", l)
	}

	open, _, _, _ := newService(t, ModeOpen)
	if _, err := open.CreateInviteLink(ctx, nil, 1, time.Hour, ""); !errors.Is(err, ErrRegistrationOpen) {
		t.Errorf("open mode: %v", err)
	}
}

func TestInviteLinkSignUp(t *testing.T) {
	ctx := context.Background()
	s, sender, q, clk := newService(t, ModeInvite)
	member, err := q.CreateUser(ctx, "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	l, token := newLink(t, s, 2)
	if exp, err := s.CheckInviteLink(ctx, token); err != nil || !exp.Equal(l.ExpiresAt) {
		t.Fatalf("check = %v, %v", exp, err)
	}

	sess, err := signUp(t, s, sender, "New@Example.com", token)
	if err != nil {
		t.Fatal(err)
	}
	if sess.User.Email != "new@example.com" || sess.User.InviteLinkID == nil || *sess.User.InviteLinkID != l.ID {
		t.Errorf("new account = %+v", sess.User)
	}
	if got, _ := q.GetInviteLink(ctx, l.ID); got.Used != 1 {
		t.Errorf("used = %d", got.Used)
	}

	// Without the link an unknown address still gets nothing.
	n := sender.count()
	if _, err := s.RequestCode(ctx, "stranger@example.com", "", noIP); err != nil || sender.count() != n {
		t.Errorf("no link: %v, %d sent", err, sender.count()-n)
	}
	// A code without the link creates no account either.
	clk.advance(ResendAfter)
	if _, err := s.RequestCode(ctx, "other@example.com", token, noIP); err != nil {
		t.Fatal(err)
	}
	_, code := sender.last(t)
	if _, err := s.Verify(ctx, "other@example.com", code, "", ""); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("verify without the link: %v", err)
	}

	// An existing account just logs in and uses no place.
	sess, err = signUp(t, s, sender, "member@example.com", token)
	if err != nil || sess.User.ID != member.ID || sess.User.InviteLinkID != nil {
		t.Fatalf("member = %+v, %v", sess.User, err)
	}
	if got, _ := q.GetInviteLink(ctx, l.ID); got.Used != 1 {
		t.Errorf("used after member = %d", got.Used)
	}

	second, err := signUp(t, s, sender, "second@example.com", token)
	if err != nil {
		t.Fatal(err)
	}
	// Full: the same answer for every address, and no email.
	n = sender.count()
	for _, email := range []string{"third@example.com", "member@example.com"} {
		if _, err := s.RequestCode(ctx, email, token, noIP); !errors.Is(err, ErrInviteLink) {
			t.Errorf("%s on a full link: %v", email, err)
		}
	}
	if _, err := s.CheckInviteLink(ctx, token); !errors.Is(err, ErrInviteLink) {
		t.Errorf("check full: %v", err)
	}
	if sender.count() != n {
		t.Errorf("full link sent %d emails", sender.count()-n)
	}
	// A member who logs in normally is not held up by the closed link.
	clk.advance(ResendAfter)
	if _, err := s.RequestCode(ctx, "member@example.com", "", noIP); err != nil {
		t.Fatal(err)
	}
	_, code = sender.last(t)
	if _, err := s.Verify(ctx, "member@example.com", code, token, ""); err != nil {
		t.Errorf("member with a full link: %v", err)
	}

	// Deleting an account gives no place back.
	if err := s.DeleteAccount(ctx, second.User.ID); err != nil {
		t.Fatal(err)
	}
	created, err := q.GetUserByEmail(ctx, "new@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := q.GetInviteLink(ctx, l.ID); got.Used != 2 || LinkStatus(got, clk.now()) != LinkFull {
		t.Errorf("after deletion = %+v", got)
	}
}

func TestInviteLinkClosed(t *testing.T) {
	ctx := context.Background()
	s, sender, q, clk := newService(t, ModeInvite)

	// A code requested in time is no use once the link expires.
	l, token := newLink(t, s, 5)
	clk.advance(24*time.Hour - 5*time.Minute)
	if _, err := s.RequestCode(ctx, "late@example.com", token, noIP); err != nil {
		t.Fatal(err)
	}
	_, code := sender.last(t)
	clk.advance(5 * time.Minute)
	if _, err := s.Verify(ctx, "late@example.com", code, token, ""); !errors.Is(err, ErrInviteLink) {
		t.Errorf("verify after expiry: %v", err)
	}
	if _, err := q.GetUserByEmail(ctx, "late@example.com"); err == nil {
		t.Error("account created after expiry")
	}
	if got, _ := q.GetInviteLink(ctx, l.ID); LinkStatus(got, clk.now()) != LinkExpired {
		t.Errorf("status = %s", LinkStatus(got, clk.now()))
	}
	if _, err := s.CheckInviteLink(ctx, token); !errors.Is(err, ErrInviteLink) {
		t.Errorf("check expired: %v", err)
	}

	l, token = newLink(t, s, 5)
	if err := s.RevokeInviteLink(ctx, l.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeInviteLink(ctx, l.ID); err != nil {
		t.Errorf("revoking twice: %v", err)
	}
	if err := s.RevokeInviteLink(ctx, uuid.Must(uuid.NewV7())); !errors.Is(err, ErrInviteNotFound) {
		t.Errorf("revoking nothing: %v", err)
	}
	if _, err := s.RequestCode(ctx, "x@example.com", token, noIP); !errors.Is(err, ErrInviteLink) {
		t.Errorf("revoked: %v", err)
	}
	if _, err := s.RequestCode(ctx, "x@example.com", "garbage", noIP); !errors.Is(err, ErrInviteLink) {
		t.Errorf("garbage token: %v", err)
	}
	// None of that counted against the address.
	if _, err := s.RequestCode(ctx, "x@example.com", "", noIP); err != nil {
		t.Errorf("address afterwards: %v", err)
	}
}

func TestInviteLinkCodeCap(t *testing.T) {
	ctx := context.Background()
	s, sender, q, clk := newService(t, ModeInvite)
	if _, err := q.CreateUser(ctx, "member@example.com"); err != nil {
		t.Fatal(err)
	}
	l, token := newLink(t, s, 1)
	capacity := int(linkCodeCap(l.MaxUses))

	// Neither a refused request for an address nor a failed email counts.
	if _, err := s.RequestCode(ctx, "a0@example.com", token, noIP); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestCode(ctx, "a0@example.com", token, noIP); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("too soon: %v", err)
	}
	sender.fail = errors.New("smtp down")
	if _, err := s.RequestCode(ctx, "a1@example.com", token, noIP); err == nil {
		t.Fatal("send failure not reported")
	}
	sender.fail = nil
	if got, _ := q.GetInviteLink(ctx, l.ID); got.CodesSent != 1 {
		t.Fatalf("codes_sent = %d", got.CodesSent)
	}
	for i := 1; i < capacity; i++ {
		if _, err := s.RequestCode(ctx, "a"+string(rune('a'+i))+"@example.com", token, noIP); err != nil {
			t.Fatal(i, err)
		}
	}
	clk.advance(ResendAfter)
	for _, email := range []string{"member@example.com", "new@example.com"} {
		if _, err := s.RequestCode(ctx, email, token, noIP); !errors.Is(err, ErrInviteLink) {
			t.Errorf("%s past the cap: %v", email, err)
		}
	}
	if sender.count() != capacity {
		t.Errorf("sent %d codes, cap %d", sender.count(), capacity)
	}
	// Codes already sent still work.
	to, code := sender.last(t)
	if _, err := s.Verify(ctx, to, code, token, ""); err != nil {
		t.Errorf("code sent before the cap: %v", err)
	}
}

// Codes through a link come from its own daily budget, for every address;
// when that runs out, nothing is counted and ordinary logins still work.
func TestInviteLinkMailBudget(t *testing.T) {
	ctx := context.Background()
	s, sender, q, clk := newService(t, ModeInvite)
	if _, err := q.CreateUser(ctx, "member@example.com"); err != nil {
		t.Fatal(err)
	}
	l, token := newLink(t, s, 5)
	for _, email := range []string{"new@example.com", "member@example.com"} {
		if _, err := s.RequestCode(ctx, email, token, noIP); err != nil {
			t.Fatal(err)
		}
		if c := sender.codes[len(sender.codes)-1]; c.to != email || !c.join {
			t.Errorf("%s: sent %+v, want a code from the invitation budget", email, c)
		}
	}

	sender.joinFail = errJoinBudget
	for _, email := range []string{"other@example.com", "member2@example.com"} {
		if _, err := s.RequestCode(ctx, email, token, noIP); !errors.Is(err, errJoinBudget) {
			t.Errorf("%s over the budget: %v", email, err)
		}
	}
	if got, _ := q.GetInviteLink(ctx, l.ID); got.CodesSent != 2 {
		t.Errorf("codes_sent = %d, want the refused requests given back", got.CodesSent)
	}
	// Without the link the member logs in as usual (after the usual wait
	// between requests, which a refused one also starts).
	clk.advance(ResendAfter)
	if _, err := s.RequestCode(ctx, "member2@example.com", "", noIP); err != nil {
		t.Errorf("ordinary login: %v", err)
	}
	sender.joinFail = nil
	// The refused requests did not use the address's daily codes.
	if quota, err := s.RequestCode(ctx, "other@example.com", token, noIP); err != nil || quota.Remaining != CodesPerDay-1 {
		t.Errorf("after the budget: %+v, %v", quota, err)
	}
}

var errJoinBudget = errors.New("join budget")

// Two addresses racing for a link's last place: one gets it.
func TestInviteLinkLastPlace(t *testing.T) {
	ctx := context.Background()
	s, sender, q, _ := newService(t, ModeInvite)
	l, token := newLink(t, s, 1)
	emails := []string{"one@example.com", "two@example.com"}
	codes := map[string]string{}
	for _, e := range emails {
		if _, err := s.RequestCode(ctx, e, token, noIP); err != nil {
			t.Fatal(err)
		}
		_, codes[e] = sender.last(t)
	}
	errs := make([]error, len(emails))
	var wg sync.WaitGroup
	for i, e := range emails {
		wg.Go(func() { _, errs[i] = s.Verify(ctx, e, codes[e], token, "") })
	}
	wg.Wait()
	ok, gone := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrInviteLink):
			gone++
		default:
			t.Error(err)
		}
	}
	if ok != 1 || gone != 1 {
		t.Errorf("results %v", errs)
	}
	if got, _ := q.GetInviteLink(ctx, l.ID); got.Used != 1 {
		t.Errorf("used = %d", got.Used)
	}
}

func TestInviteLinkOpenMode(t *testing.T) {
	ctx := context.Background()
	s, sender, _, _ := newService(t, ModeOpen)
	if _, err := s.CheckInviteLink(ctx, "anything"); !errors.Is(err, ErrRegistrationOpen) {
		t.Errorf("check: %v", err)
	}
	// The token is ignored: anyone signs up as usual.
	sess, err := signUp(t, s, sender, "free@example.com", "anything")
	if err != nil || sess.User.InviteLinkID != nil {
		t.Errorf("open sign-up = %+v, %v", sess.User, err)
	}
}
