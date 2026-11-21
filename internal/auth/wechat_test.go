package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/MamaShip/Observer/internal/store/db"
)

func newUser(t *testing.T, q *db.Queries, email string) db.User {
	t.Helper()
	u, err := q.CreateUser(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func mustBindLink(t *testing.T, s *Service, openid, pending string) string {
	t.Helper()
	tok, err := s.NewWechatBindLink(context.Background(), openid, pending)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func boundTo(t *testing.T, s *Service, openid string) string {
	t.Helper()
	b, ok, err := s.WechatBindingFor(context.Background(), openid)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return ""
	}
	return b.User.Email
}

func TestWechatBind(t *testing.T) {
	ctx := context.Background()
	s, _, q, clk := newService(t, ModeInvite)
	alice := newUser(t, q, "alice@example.com")

	if got := boundTo(t, s, "oA"); got != "" {
		t.Fatalf("bound before binding: %s", got)
	}
	tok := mustBindLink(t, s, "oA", "https://mp.weixin.qq.com/s/abc")
	pending, err := s.BindWechat(ctx, alice.ID, tok)
	if err != nil || pending != "https://mp.weixin.qq.com/s/abc" {
		t.Fatalf("bind: %q %v", pending, err)
	}
	if got := boundTo(t, s, "oA"); got != "alice@example.com" {
		t.Fatalf("bound to %q", got)
	}
	if since, err := s.WechatBoundSince(ctx, alice.ID); err != nil || since == nil {
		t.Fatalf("bound since: %v %v", since, err)
	}
	// One use only.
	if _, err := s.BindWechat(ctx, alice.ID, tok); !errors.Is(err, ErrWechatLink) {
		t.Errorf("second use: %v", err)
	}
	for _, bad := range []string{"", "short", tok[:42] + "!"} {
		if _, err := s.BindWechat(ctx, alice.ID, bad); !errors.Is(err, ErrWechatLink) {
			t.Errorf("token %q: %v", bad, err)
		}
	}
	// Expiry.
	tok = mustBindLink(t, s, "oB", "")
	clk.advance(WechatBindTTL + time.Second)
	if _, err := s.BindWechat(ctx, alice.ID, tok); !errors.Is(err, ErrWechatLink) {
		t.Errorf("expired: %v", err)
	}
	// A disabled account cannot bind, and the link is not used up.
	if _, err := q.SetUserStatus(ctx, db.SetUserStatusParams{Email: "alice@example.com", Status: "disabled"}); err != nil {
		t.Fatal(err)
	}
	tok = mustBindLink(t, s, "oB", "")
	if _, err := s.BindWechat(ctx, alice.ID, tok); !errors.Is(err, ErrDisabled) {
		t.Errorf("disabled: %v", err)
	}
	if _, err := q.SetUserStatus(ctx, db.SetUserStatusParams{Email: "alice@example.com", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if pending, err := s.BindWechat(ctx, alice.ID, tok); err != nil || pending != "" {
		t.Errorf("after enabling: %q %v", pending, err)
	}
}

func TestWechatRebind(t *testing.T) {
	ctx := context.Background()
	s, _, q, _ := newService(t, ModeInvite)
	alice, bob := newUser(t, q, "alice@example.com"), newUser(t, q, "bob@example.com")

	if _, err := s.BindWechat(ctx, alice.ID, mustBindLink(t, s, "oA", "")); err != nil {
		t.Fatal(err)
	}
	login, err := s.NewWechatLoginLink(ctx, "oA", alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The same WeChat user moves to Bob: Alice is no longer bound, and the
	// login link sent for Alice does not sign anyone in.
	if _, err := s.BindWechat(ctx, bob.ID, mustBindLink(t, s, "oA", "")); err != nil {
		t.Fatal(err)
	}
	if got := boundTo(t, s, "oA"); got != "bob@example.com" {
		t.Fatalf("oA bound to %q", got)
	}
	if since, _ := s.WechatBoundSince(ctx, alice.ID); since != nil {
		t.Error("alice still bound")
	}
	if _, err := s.WechatLogin(ctx, login, ""); !errors.Is(err, ErrWechatLink) {
		t.Errorf("stale login link: %v", err)
	}
	// Bob binds another WeChat user: one binding per account.
	if _, err := s.BindWechat(ctx, bob.ID, mustBindLink(t, s, "oB", "")); err != nil {
		t.Fatal(err)
	}
	if boundTo(t, s, "oA") != "" || boundTo(t, s, "oB") != "bob@example.com" {
		t.Errorf("after bob rebinds: oA=%q oB=%q", boundTo(t, s, "oA"), boundTo(t, s, "oB"))
	}
}

func TestWechatBindConcurrent(t *testing.T) {
	ctx := context.Background()
	s, _, q, _ := newService(t, ModeInvite)
	users := []db.User{newUser(t, q, "a@example.com"), newUser(t, q, "b@example.com"), newUser(t, q, "c@example.com")}
	toks := make([]string, len(users))
	for i := range users {
		toks[i] = mustBindLink(t, s, "oA", "")
	}
	var wg sync.WaitGroup
	errs := make([]error, len(users))
	for i, u := range users {
		wg.Go(func() { _, errs[i] = s.BindWechat(ctx, u.ID, toks[i]) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("bind %d: %v", i, err)
		}
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM wechat_bindings`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 || boundTo(t, s, "oA") == "" {
		t.Errorf("%d bindings after concurrent binds", n)
	}
}

func TestWechatLogin(t *testing.T) {
	ctx := context.Background()
	s, _, q, clk := newService(t, ModeInvite)
	alice := newUser(t, q, "alice@example.com")
	if _, err := s.WechatLogin(ctx, "nope", ""); !errors.Is(err, ErrWechatLink) {
		t.Errorf("bad token: %v", err)
	}
	if _, err := s.BindWechat(ctx, alice.ID, mustBindLink(t, s, "oA", "")); err != nil {
		t.Fatal(err)
	}
	// A bind link is not a login link.
	if _, err := s.WechatLogin(ctx, mustBindLink(t, s, "oA", ""), ""); !errors.Is(err, ErrWechatLink) {
		t.Errorf("bind link used to log in: %v", err)
	}
	tok, err := s.NewWechatLoginLink(ctx, "oA", alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.WechatLogin(ctx, tok, "WeChat")
	if err != nil {
		t.Fatal(err)
	}
	if u, err := s.Authenticate(ctx, sess.Token); err != nil || u.ID != alice.ID {
		t.Fatalf("session: %v %v", u.Email, err)
	}
	if _, err := s.WechatLogin(ctx, tok, ""); !errors.Is(err, ErrWechatLink) {
		t.Errorf("second use: %v", err)
	}

	tok, _ = s.NewWechatLoginLink(ctx, "oA", alice.ID)
	clk.advance(WechatLoginTTL + time.Second)
	if _, err := s.WechatLogin(ctx, tok, ""); !errors.Is(err, ErrWechatLink) {
		t.Errorf("expired: %v", err)
	}

	tok, _ = s.NewWechatLoginLink(ctx, "oA", alice.ID)
	if _, err := q.SetUserStatus(ctx, db.SetUserStatusParams{Email: "alice@example.com", Status: "disabled"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WechatLogin(ctx, tok, ""); !errors.Is(err, ErrDisabled) {
		t.Errorf("disabled: %v", err)
	}
	if _, err := q.SetUserStatus(ctx, db.SetUserStatusParams{Email: "alice@example.com", Status: "active"}); err != nil {
		t.Fatal(err)
	}

	// Unbinding from the website voids the links.
	tok, _ = s.NewWechatLoginLink(ctx, "oA", alice.ID)
	if err := s.UnbindWechat(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UnbindWechat(ctx, alice.ID); err != nil {
		t.Errorf("unbinding twice: %v", err)
	}
	if _, err := s.WechatLogin(ctx, tok, ""); !errors.Is(err, ErrWechatLink) {
		t.Errorf("after unbinding: %v", err)
	}
}

// wechatTraces counts rows that still mention openid.
func wechatTraces(t *testing.T, s *Service, openid string) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM wechat_bindings WHERE openid = $1) +
		(SELECT count(*) FROM wechat_tokens WHERE openid = $1)`, openid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestForgetWechat(t *testing.T) {
	ctx := context.Background()
	s, _, q, _ := newService(t, ModeInvite)
	alice := newUser(t, q, "alice@example.com")
	if _, err := s.BindWechat(ctx, alice.ID, mustBindLink(t, s, "oA", "")); err != nil {
		t.Fatal(err)
	}
	mustBindLink(t, s, "oA", "https://mp.weixin.qq.com/s/abc")
	if _, err := s.NewWechatLoginLink(ctx, "oA", alice.ID); err != nil {
		t.Fatal(err)
	}
	if had, err := s.ForgetWechat(ctx, "oA"); err != nil || !had {
		t.Fatalf("forget: %v %v", had, err)
	}
	if had, err := s.ForgetWechat(ctx, "oA"); err != nil || had {
		t.Errorf("forget again: %v %v", had, err)
	}
	if n := wechatTraces(t, s, "oA"); n != 0 {
		t.Errorf("%d rows still mention the OpenID", n)
	}
}

func TestDeleteAccountForgetsWechat(t *testing.T) {
	ctx := context.Background()
	s, _, q, _ := newService(t, ModeInvite)
	alice := newUser(t, q, "alice@example.com")
	if _, err := s.BindWechat(ctx, alice.ID, mustBindLink(t, s, "oA", "")); err != nil {
		t.Fatal(err)
	}
	mustBindLink(t, s, "oA", "")
	if _, err := s.NewWechatLoginLink(ctx, "oA", alice.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	if n := wechatTraces(t, s, "oA"); n != 0 {
		t.Errorf("%d rows still mention the OpenID", n)
	}
}

func TestCleanupWechatLinks(t *testing.T) {
	ctx := context.Background()
	s, _, _, clk := newService(t, ModeInvite)
	mustBindLink(t, s, "oA", "")
	clk.advance(WechatBindTTL + time.Second)
	mustBindLink(t, s, "oB", "")
	if err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if wechatTraces(t, s, "oA") != 0 || wechatTraces(t, s, "oB") != 1 {
		t.Errorf("after cleanup: oA=%d oB=%d", wechatTraces(t, s, "oA"), wechatTraces(t, s, "oB"))
	}
}
