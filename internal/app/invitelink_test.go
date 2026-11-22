package app

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MamaShip/Observer/internal/api"
	"github.com/MamaShip/Observer/internal/auth"
	"github.com/MamaShip/Observer/internal/config"
	"github.com/MamaShip/Observer/internal/mail"
	"github.com/MamaShip/Observer/internal/store/storetest"
	"github.com/MamaShip/Observer/internal/watch"
	"github.com/MamaShip/Observer/internal/watch/watchtest"
)

// joinWith signs email up through a public invitation link, as /join does.
func (c *client) joinWith(mailer *mail.Memory, email, token string) {
	c.t.Helper()
	before := len(mailer.Sent())
	c.api(http.MethodPost, "/auth/code", map[string]string{"email": email, "invite_token": token}, http.StatusAccepted, nil)
	var code string
	for _, msg := range mailer.Sent()[before:] {
		if m := codeRe.FindStringSubmatch(msg.Text); msg.To == email && m != nil {
			code = m[1]
		}
	}
	if code == "" {
		c.t.Fatalf("login code not sent to %s", email)
	}
	resp, b := c.do(http.MethodPost, site, api.Prefix+"/auth/verify", map[string]string{"email": email, "code": code, "invite_token": token})
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("verify %s: %d %s", email, resp.StatusCode, b)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == auth.CookieName {
			c.cookie = ck.Value
		}
	}
}

// Public invitation links: an admin opens one, people sign up through it
// until its places run out, and the console tells link sign-ups apart from
// personal invitations.
func TestInvitationLinks(t *testing.T) {
	web := watchtest.NewWeb()
	mailer := mail.NewMemory(nil)
	cfg := config.Config{
		DatabaseURL: storetest.EmptyDB(t), DBMaxConns: 8, BlobDir: t.TempDir(),
		WatchDays: 60, FetchMinInterval: time.Second,
		BaseURL: "http://" + site, SnapBaseURL: "http://" + snap, SigningKey: strings.Repeat("s", 32),
		RegistrationMode: auth.ModeInvite, ServiceName: "折光观察者", DailyEmailLimit: 100, MonthlyEmailLimit: 20,
		MaxOpenWatches: 50, MaxDailySubmissions: 30, MonthlyInviteLimit: 2,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := New(ctx, cfg, storetest.Logger(), "test", Overrides{
		NewSession: func() watch.Session { return web.Session() }, Mailer: mailer,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx, ln) }()

	if _, err := a.pool.Exec(ctx, `INSERT INTO users (email, role) VALUES
		('member@example.com', 'user'), ('ops@example.com', 'admin')`); err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ops := &client{t: t, addr: addr}
	ops.login(mailer, "ops@example.com")
	member := &client{t: t, addr: addr}
	member.login(mailer, "member@example.com")

	// Only admins manage links.
	member.api(http.MethodGet, "/admin/invite-links", nil, http.StatusForbidden, nil)
	member.api(http.MethodPost, "/admin/invite-links", map[string]any{"max_uses": 2, "valid_hours": 24}, http.StatusForbidden, nil)
	ops.api(http.MethodPost, "/admin/invite-links", map[string]any{"max_uses": 0, "valid_hours": 24}, http.StatusUnprocessableEntity, nil)

	var link api.InviteLink
	ops.api(http.MethodPost, "/admin/invite-links", map[string]any{"max_uses": 2, "valid_hours": 24, "note": "读者群"}, http.StatusCreated, &link)
	prefix := "http://" + site + "/join#t="
	if link.Status != auth.LinkActive || link.MaxUses != 2 || link.Note != "读者群" || link.CreatedBy != "ops@example.com" ||
		!strings.HasPrefix(link.URL, prefix) || time.Until(link.ExpiresAt) < 23*time.Hour {
		t.Fatalf("link = %+v", link)
	}
	token := strings.TrimPrefix(link.URL, prefix)

	visitor := &client{t: t, addr: addr}
	var check struct {
		ExpiresAt time.Time `json:"expires_at"`
	}
	visitor.api(http.MethodPost, "/auth/invite-link", map[string]string{"token": token}, http.StatusOK, &check)
	if !check.ExpiresAt.Equal(link.ExpiresAt) {
		t.Errorf("expires_at = %v, want %v", check.ExpiresAt, link.ExpiresAt)
	}
	visitor.api(http.MethodPost, "/auth/invite-link", map[string]string{"token": "nope"}, http.StatusGone, nil)

	visitor.joinWith(mailer, "reader@example.com", token)
	var me api.Me
	visitor.api(http.MethodGet, "/me", nil, http.StatusOK, &me)
	if me.Email != "reader@example.com" {
		t.Fatalf("signed up as %s", me.Email)
	}
	(&client{t: t, addr: addr}).joinWith(mailer, "second@example.com", token)

	// Full: one answer for every address, and the page says so.
	for _, email := range []string{"third@example.com", "member@example.com"} {
		visitor.api(http.MethodPost, "/auth/code", map[string]string{"email": email, "invite_token": token}, http.StatusGone, nil)
	}
	visitor.api(http.MethodPost, "/auth/invite-link", map[string]string{"token": token}, http.StatusGone, nil)

	var links struct {
		Items []api.InviteLink `json:"items"`
	}
	ops.api(http.MethodGet, "/admin/invite-links", nil, http.StatusOK, &links)
	if len(links.Items) != 1 || links.Items[0].Status != auth.LinkFull || links.Items[0].Used != 2 ||
		links.Items[0].Accounts != 2 || links.Items[0].URL != link.URL {
		t.Errorf("links = %+v", links.Items)
	}

	// The console: these accounts came from the link, not from a person.
	var users struct {
		Items []api.AdminUser `json:"items"`
	}
	ops.api(http.MethodGet, "/admin/users?invite_link="+link.ID.String(), nil, http.StatusOK, &users)
	if len(users.Items) != 2 {
		t.Fatalf("link's accounts = %+v", users.Items)
	}
	for _, u := range users.Items {
		if u.InviteLinkID == nil || *u.InviteLinkID != link.ID || u.InviteLinkNote != "读者群" || u.Invited || u.InvitedBy != "" {
			t.Errorf("account %+v", u)
		}
	}
	ops.api(http.MethodGet, "/admin/users?invite_link=nope", nil, http.StatusBadRequest, nil)

	// Revoking closes a link at once.
	var open api.InviteLink
	ops.api(http.MethodPost, "/admin/invite-links", map[string]any{"max_uses": 5, "valid_hours": 1}, http.StatusCreated, &open)
	ops.api(http.MethodDelete, "/admin/invite-links/"+open.ID.String(), nil, http.StatusNoContent, nil)
	visitor.api(http.MethodPost, "/auth/invite-link", map[string]string{"token": strings.TrimPrefix(open.URL, prefix)}, http.StatusGone, nil)
	ops.api(http.MethodDelete, "/admin/invite-links/00000000-0000-7000-8000-000000000000", nil, http.StatusNotFound, nil)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}
