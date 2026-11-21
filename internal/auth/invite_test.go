package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestMonthStart(t *testing.T) {
	// 2026-11-30 17:00 UTC is already December 1st in Beijing.
	got := MonthStart(time.Date(2026, 11, 30, 17, 0, 0, 0, time.UTC))
	if want := time.Date(2026, 12, 1, 0, 0, 0, 0, Beijing); !got.Equal(want) {
		t.Errorf("MonthStart = %v, want %v", got, want)
	}
}

func TestInvite(t *testing.T) {
	ctx := context.Background()
	s, sender, q, clk := newService(t, ModeInvite)
	alice, err := q.CreateUser(ctx, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateUser(ctx, "member@example.com"); err != nil {
		t.Fatal(err)
	}
	const limit = 3

	if _, _, err := s.Invite(ctx, alice.ID, "Alice@Example.com", limit); !errors.Is(err, ErrInviteSelf) {
		t.Errorf("inviting oneself: %v", err)
	}
	if _, _, err := s.Invite(ctx, alice.ID, "not an address", limit); !errors.Is(err, ErrInvalidEmail) {
		t.Errorf("bad address: %v", err)
	}

	inv, quota, err := s.Invite(ctx, alice.ID, "Bob@Example.com", limit)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Email != "bob@example.com" || inv.InviteeID == nil || quota != (InviteQuota{Used: 1, Limit: limit}) {
		t.Errorf("invite = %+v, quota %+v", inv, quota)
	}
	// The invited address can now log in.
	if _, err := s.RequestCode(ctx, "bob@example.com", ip); err != nil || sender.count() != 1 {
		t.Fatalf("invitee's code: %v, %d sent", err, sender.count())
	}

	// Repeating an invitation is free.
	again, quota, err := s.Invite(ctx, alice.ID, "bob@example.com", limit)
	if err != nil || again.ID != inv.ID || quota.Used != 1 {
		t.Errorf("repeat = %+v, %+v, %v", again, quota, err)
	}

	// An address with an account counts the same and is not linked.
	member, quota, err := s.Invite(ctx, alice.ID, "member@example.com", limit)
	if err != nil || member.InviteeID != nil || quota.Used != 2 {
		t.Errorf("existing account = %+v, %+v, %v", member, quota, err)
	}

	if _, _, err := s.Invite(ctx, alice.ID, "carol@example.com", limit); err != nil {
		t.Fatal(err)
	}
	if _, quota, err := s.Invite(ctx, alice.ID, "dave@example.com", limit); !errors.Is(err, ErrNoInvitesLeft) || quota.Used != 3 {
		t.Errorf("over the limit: %+v, %v", quota, err)
	}
	if _, err := q.GetUserByEmail(ctx, "dave@example.com"); err == nil {
		t.Error("a refused invitation created the account")
	}
	if quota, err := s.InviteQuotaOf(ctx, alice.ID, limit); err != nil || quota.Used != 3 {
		t.Errorf("quota = %+v, %v", quota, err)
	}

	// A new Beijing month brings a new allowance. The clock starts on
	// 2026-11-20; December 1st begins at 16:00 UTC on November 30th.
	clk.t = time.Date(2026, 11, 30, 16, 0, 1, 0, time.UTC)
	if _, quota, err := s.Invite(ctx, alice.ID, "dave@example.com", limit); err != nil || quota.Used != 1 {
		t.Errorf("next month: %+v, %v", quota, err)
	}
	// No limit.
	for _, e := range []string{"e1@example.com", "e2@example.com", "e3@example.com", "e4@example.com"} {
		if _, _, err := s.Invite(ctx, alice.ID, e, 0); err != nil {
			t.Fatalf("unlimited: %v", err)
		}
	}
}

func TestInvitesForgottenWithAccounts(t *testing.T) {
	ctx := context.Background()
	s, _, q, _ := newService(t, ModeInvite)
	alice, _ := q.CreateUser(ctx, "alice@example.com")
	if _, err := q.CreateUser(ctx, "member@example.com"); err != nil {
		t.Fatal(err)
	}
	for _, e := range []string{"bob@example.com", "carol@example.com", "member@example.com"} {
		if _, _, err := s.Invite(ctx, alice.ID, e, 0); err != nil {
			t.Fatal(err)
		}
	}
	count := func(where string) int {
		var n int
		if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM invites WHERE "+where).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Bob deletes his account: his address leaves the invitations.
	bob, _ := q.GetUserByEmail(ctx, "bob@example.com")
	if err := s.DeleteAccount(ctx, bob.ID); err != nil {
		t.Fatal(err)
	}
	if n := count("email = 'bob@example.com'"); n != 0 {
		t.Errorf("%d invitations still name a deleted account", n)
	}
	// Alice deletes hers: Carol stays recorded as invited (by a deleted
	// account); the invitation to an existing account, which recorded
	// nothing but the address, goes.
	if err := s.DeleteAccount(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	if n := count("inviter_id IS NULL AND email = 'carol@example.com' AND invitee_id IS NOT NULL"); n != 1 {
		t.Errorf("carol's invitation: %d rows", n)
	}
	if n := count("email = 'member@example.com'"); n != 0 {
		t.Errorf("%d invitations to an existing account kept", n)
	}
}

func TestCreateInvited(t *testing.T) {
	ctx := context.Background()
	s, _, q, _ := newService(t, ModeInvite)
	admin, _ := q.CreateUser(ctx, "admin@example.com")
	u, created, err := s.CreateInvited(ctx, &admin.ID, "New@Example.com")
	if err != nil || !created || u.Email != "new@example.com" {
		t.Fatalf("created = %v, %+v, %v", created, u, err)
	}
	again, created, err := s.CreateInvited(ctx, &admin.ID, "new@example.com")
	if err != nil || created || again.ID != u.ID {
		t.Errorf("again = %v, %+v, %v", created, again, err)
	}
	if _, created, err := s.CreateInvited(ctx, nil, "cli@example.com"); err != nil || !created {
		t.Errorf("without inviter: %v, %v", created, err)
	}
	var invitee uuid.UUID
	if err := s.pool.QueryRow(ctx, "SELECT invitee_id FROM invites WHERE inviter_id = $1", admin.ID).Scan(&invitee); err != nil || invitee != u.ID {
		t.Errorf("recorded invitee = %v, %v", invitee, err)
	}
	var n int
	_ = s.pool.QueryRow(ctx, "SELECT count(*) FROM invites").Scan(&n)
	if n != 1 {
		t.Errorf("%d invitations recorded", n)
	}
}

func TestRevokeInvite(t *testing.T) {
	ctx := context.Background()
	s, sender, q, _ := newService(t, ModeInvite)
	alice, _ := q.CreateUser(ctx, "alice@example.com")
	carol, _ := q.CreateUser(ctx, "carol@example.com")
	if _, err := q.CreateUser(ctx, "member@example.com"); err != nil {
		t.Fatal(err)
	}
	const limit = 3
	status := func(inviter uuid.UUID) map[string]bool {
		rows, err := q.ListInvitesByInviter(ctx, &inviter)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]bool{}
		for _, r := range rows {
			m[r.Email] = r.Accepted
		}
		return m
	}

	bob, _, err := s.Invite(ctx, alice.ID, "bob@example.com", limit)
	if err != nil {
		t.Fatal(err)
	}
	member, _, err := s.Invite(ctx, alice.ID, "member@example.com", limit)
	if err != nil {
		t.Fatal(err)
	}
	if got := status(alice.ID); len(got) != 2 || got["bob@example.com"] || got["member@example.com"] {
		t.Errorf("before login: %v", got)
	}

	// Someone else's invitation is not found.
	if err := s.RevokeInvite(ctx, carol.ID, bob.ID); !errors.Is(err, ErrInviteNotFound) {
		t.Errorf("other inviter: %v", err)
	}
	// An invitation to an existing account goes, and the account stays.
	if err := s.RevokeInvite(ctx, alice.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetUserByEmail(ctx, "member@example.com"); err != nil {
		t.Errorf("existing account: %v", err)
	}
	if quota, _ := s.InviteQuotaOf(ctx, alice.ID, limit); quota.Used != 1 {
		t.Errorf("allowance after revoking: %+v", quota)
	}

	// Carol invites bob too; when alice withdraws, carol's invitation holds
	// the account.
	carolsBob, _, err := s.Invite(ctx, carol.ID, "bob@example.com", limit)
	if err != nil || carolsBob.InviteeID != nil {
		t.Fatalf("second invitation: %+v, %v", carolsBob, err)
	}
	if err := s.RevokeInvite(ctx, alice.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetUserByEmail(ctx, "bob@example.com"); err != nil {
		t.Fatalf("bob's account went although carol invited him: %v", err)
	}

	// Bob logs in: carol's invitation is accepted and cannot be revoked.
	if _, err := s.RequestCode(ctx, "bob@example.com", ip); err != nil {
		t.Fatal(err)
	}
	_, code := sender.last(t)
	if _, err := s.Verify(ctx, "bob@example.com", code, ""); err != nil {
		t.Fatal(err)
	}
	if got := status(carol.ID); !got["bob@example.com"] {
		t.Errorf("after login: %v", got)
	}
	if err := s.RevokeInvite(ctx, carol.ID, carolsBob.ID); !errors.Is(err, ErrInviteAccepted) {
		t.Errorf("accepted: %v", err)
	}

	// The only invitation of an address that never logged in takes the
	// account with it.
	dave, _, err := s.Invite(ctx, alice.ID, "dave@example.com", limit)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeInvite(ctx, alice.ID, dave.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetUserByEmail(ctx, "dave@example.com"); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("dave's account: %v", err)
	}
	if err := s.RevokeInvite(ctx, alice.ID, dave.ID); !errors.Is(err, ErrInviteNotFound) {
		t.Errorf("twice: %v", err)
	}
}
