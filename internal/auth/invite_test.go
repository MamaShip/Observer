package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MamaShip/Observer/internal/store/db"
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

func TestInviteFromCLI(t *testing.T) {
	ctx := context.Background()
	s, _, q, _ := newService(t, ModeInvite)
	for range 2 {
		if err := s.InviteFromCLI(ctx, "New@Example.com"); err != nil {
			t.Fatal(err)
		}
	}
	inv, err := q.GetInviteByInviter(ctx, db.GetInviteByInviterParams{Email: "new@example.com"})
	if err != nil || !inv.Invite.MailQueued || inv.Invite.InviteeID != nil {
		t.Fatalf("operator invitation: %+v, %v", inv, err)
	}
	var jobs int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM river_job WHERE kind='email_invite'").Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("queued %d jobs: %v", jobs, err)
	}
}

func TestInvitationLinks(t *testing.T) {
	ctx := context.Background()
	s, sender, q, clk := newService(t, ModeInvite)
	owner, err := q.CreateUser(ctx, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	invite := func(email string) (uuid.UUID, string) {
		t.Helper()
		inv, _, err := s.Invite(ctx, owner.ID, email, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.DeliverInvite(ctx, inv.ID); err != nil {
			t.Fatal(err)
		}
		return inv.ID, sender.inviteToken
	}
	revoked, token := invite("revoked@example.com")
	if err := s.RevokeInvite(ctx, owner.ID, revoked); err != nil {
		t.Fatal(err)
	}
	_, replacement := invite("revoked@example.com")
	assertGone := func(token string) {
		t.Helper()
		if _, err := s.AcceptInvite(ctx, token, ""); !errors.Is(err, ErrInviteLink) {
			t.Fatalf("expected invalid invitation, got %v", err)
		}
	}
	assertGone(token)
	results := make(chan error, 2)
	for range 2 {
		go func() { _, err := s.AcceptInvite(ctx, replacement, ""); results <- err }()
	}
	a, b := <-results, <-results
	if b == nil {
		a, b = b, a
	}
	if a != nil || !errors.Is(b, ErrInviteLink) {
		t.Fatalf("concurrent acceptance: %v, %v", a, b)
	}
	_, expired := invite("expired@example.com")
	jobs := func() (n int) {
		t.Helper()
		if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM river_job WHERE kind = 'email_invite'").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	queued := jobs()
	if _, _, err := s.Invite(ctx, owner.ID, "expired@example.com", 0); err != nil || jobs() != queued {
		t.Fatalf("repeat while the link is valid: %d jobs, %v", jobs()-queued, err)
	}
	clk.advance(InviteLinkTTL)
	assertGone(expired)
	// Repeating the invitation once the link expired sends a fresh one.
	_, fresh := invite("expired@example.com")
	if fresh == expired || jobs() != queued+1 {
		t.Fatalf("no fresh link: %d jobs", jobs()-queued)
	}
	if _, err := s.AcceptInvite(ctx, fresh, ""); err != nil {
		t.Fatalf("fresh link: %v", err)
	}
	_, disabled := invite("disabled@example.com")
	if _, err := q.SetUserStatus(ctx, db.SetUserStatusParams{Email: "disabled@example.com", Status: "disabled"}); err != nil {
		t.Fatal(err)
	}
	assertGone(disabled)

	_, byCode := invite("code@example.com")
	if _, err := s.RequestCode(ctx, "code@example.com", ip); err != nil {
		t.Fatal(err)
	}
	email, code := sender.last(t)
	if _, err := s.Verify(ctx, email, code, ""); err != nil {
		t.Fatal(err)
	}
	assertGone(byCode)
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
	// Its email was queued, so its place stays counted.
	if quota, _ := s.InviteQuotaOf(ctx, alice.ID, limit); quota.Used != 2 {
		t.Errorf("allowance after revoking: %+v", quota)
	}
	// An invitation from before invitation emails gives its place back.
	if _, err := q.CreateUser(ctx, "early@example.com"); err != nil {
		t.Fatal(err)
	}
	var early uuid.UUID
	if err := s.pool.QueryRow(ctx, "INSERT INTO invites (inviter_id, email) VALUES ($1, 'early@example.com') RETURNING id", alice.ID).Scan(&early); err != nil {
		t.Fatal(err)
	}
	if quota, _ := s.InviteQuotaOf(ctx, alice.ID, limit); quota.Used != 3 {
		t.Errorf("allowance with the early invitation: %+v", quota)
	}
	if err := s.RevokeInvite(ctx, alice.ID, early); err != nil {
		t.Fatal(err)
	}
	if quota, _ := s.InviteQuotaOf(ctx, alice.ID, limit); quota.Used != 2 {
		t.Errorf("allowance after revoking the early invitation: %+v", quota)
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
