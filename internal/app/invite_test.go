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

// Invitations in invite mode: a user invites within a monthly allowance,
// the invitee can log in, and the admin console shows who invited whom.
func TestInvitations(t *testing.T) {
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
		('alice@example.com', 'user'), ('member@example.com', 'user'), ('ops@example.com', 'admin')`); err != nil {
		t.Fatal(err)
	}
	alice := &client{t: t, addr: ln.Addr().String()}
	alice.login(mailer, "alice@example.com")

	var invs api.Invitations
	alice.api(http.MethodGet, "/invites", nil, 200, &invs)
	if !invs.CanInvite || invs.MonthlyLimit != 2 || invs.UsedThisMonth != 0 || len(invs.Items) != 0 {
		t.Errorf("before = %+v", invs)
	}
	type answer struct {
		Invitation    api.Invitation `json:"invitation"`
		UsedThisMonth int            `json:"used_this_month"`
		MonthlyLimit  int            `json:"monthly_limit"`
	}
	var got answer
	alice.api(http.MethodPost, "/invites", map[string]string{"email": "Bob@Example.com"}, http.StatusCreated, &got)
	if got.Invitation.Email != "bob@example.com" || got.UsedThisMonth != 1 || got.MonthlyLimit != 2 {
		t.Errorf("invite = %+v", got)
	}
	alice.api(http.MethodPost, "/invites", map[string]string{"email": "alice@example.com"}, http.StatusUnprocessableEntity, nil)
	// An address that already has an account gets the same answer.
	alice.api(http.MethodPost, "/invites", map[string]string{"email": "member@example.com"}, http.StatusCreated, &got)
	if got.Invitation.Email != "member@example.com" || got.UsedThisMonth != 2 {
		t.Errorf("existing account = %+v", got)
	}
	alice.api(http.MethodPost, "/invites", map[string]string{"email": "carol@example.com"}, http.StatusTooManyRequests, nil)
	alice.api(http.MethodPost, "/invites", map[string]string{"email": "bob@example.com"}, http.StatusCreated, &got) // a repeat is free
	alice.api(http.MethodGet, "/invites", nil, 200, &invs)
	if invs.UsedThisMonth != 2 || len(invs.Items) != 2 || invs.Items[0].Email != "member@example.com" {
		t.Errorf("after = %+v", invs)
	}

	// Bob can log in now; Carol cannot.
	(&client{t: t, addr: alice.addr}).login(mailer, "bob@example.com")
	before := len(mailer.Sent())
	(&client{t: t, addr: alice.addr}).api(http.MethodPost, "/auth/code", map[string]string{"email": "carol@example.com"}, http.StatusAccepted, nil)
	if len(mailer.Sent()) != before {
		t.Error("an address nobody invited got a login code")
	}

	// The admin console: who invited whom. Admins invite without a limit.
	ops := &client{t: t, addr: alice.addr}
	ops.login(mailer, "ops@example.com")
	ops.api(http.MethodGet, "/invites", nil, 200, &invs)
	if !invs.CanInvite || invs.MonthlyLimit != 0 {
		t.Errorf("admin's allowance = %+v", invs)
	}
	var users struct {
		Items []api.AdminUser `json:"items"`
	}
	ops.api(http.MethodGet, "/admin/users?search=alice", nil, 200, &users)
	if len(users.Items) != 1 || users.Items[0].Invitees != 1 || users.Items[0].InvitesThisMonth != 2 || users.Items[0].Invited {
		t.Fatalf("alice = %+v", users.Items)
	}
	aliceID := users.Items[0].ID
	users.Items = nil
	ops.api(http.MethodGet, "/admin/users?invited_by="+aliceID.String(), nil, 200, &users)
	if len(users.Items) != 1 || users.Items[0].Email != "bob@example.com" || !users.Items[0].Invited ||
		users.Items[0].InvitedBy != "alice@example.com" || users.Items[0].InvitedByID == nil || *users.Items[0].InvitedByID != aliceID {
		t.Errorf("alice's invitees = %+v", users.Items)
	}
	ops.api(http.MethodGet, "/admin/users?invited_by=nope", nil, http.StatusBadRequest, nil)
	var dave api.AdminUser
	ops.api(http.MethodPost, "/admin/users", map[string]string{"email": "dave@example.com"}, http.StatusCreated, &dave)
	if dave.InvitedBy != "ops@example.com" {
		t.Errorf("admin's invitee = %+v", dave)
	}

	// Alice deletes her account: Bob is still marked as invited, by nobody
	// who still exists.
	alice.api(http.MethodDelete, "/me", nil, http.StatusNoContent, nil)
	users.Items = nil // fields absent from a response keep their old values
	ops.api(http.MethodGet, "/admin/users?search=bob", nil, 200, &users)
	if len(users.Items) != 1 || !users.Items[0].Invited || users.Items[0].InvitedBy != "" || users.Items[0].InvitedByID != nil {
		t.Errorf("bob after alice left = %+v", users.Items)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}
