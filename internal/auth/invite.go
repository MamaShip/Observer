package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MamaShip/Observer/internal/store/db"
)

// Invitations (plan.md §6.1). In invite mode only existing accounts receive
// login codes, so inviting an address means creating its account. Nobody is
// emailed: the inviter tells the person, who then logs in as usual.

// Errors from Invite.
var (
	ErrInviteSelf    = errors.New("that is the inviter's own address")
	ErrNoInvitesLeft = errors.New("no invitations left this month")
)

// MonthStart is the start of t's calendar month in Beijing time, when the
// monthly allowances (notification emails, invitations) reset.
func MonthStart(t time.Time) time.Time {
	y, m, _ := t.In(Beijing).Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, Beijing)
}

// InviteQuota is a user's invitations this month.
type InviteQuota struct {
	Used  int
	Limit int // 0: no limit
}

// Invite lets email log in, on behalf of the user inviterID, who may send
// limit invitations per Beijing month (0: no limit). Every invitation counts,
// whether or not the address already had an account, and the result is the
// same either way; only repeating one of the user's own invitations is free.
// An existing account is never changed.
func (s *Service) Invite(ctx context.Context, inviterID uuid.UUID, email string, limit int) (db.Invite, InviteQuota, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return db.Invite{}, InviteQuota{}, err
	}
	var inv db.Invite
	quota := InviteQuota{Limit: limit}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		inviter, err := q.LockUser(ctx, inviterID)
		if err != nil {
			return err
		}
		if inviter.Email == email {
			return ErrInviteSelf
		}
		used, err := q.CountInvitesSince(ctx, db.CountInvitesSinceParams{InviterID: &inviterID, Since: MonthStart(s.now())})
		if err != nil {
			return err
		}
		quota.Used = int(used)
		inv, err = q.GetInviteByInviter(ctx, db.GetInviteByInviterParams{InviterID: &inviterID, Email: email})
		if err == nil {
			return nil // already invited by this user
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if limit > 0 && quota.Used >= limit {
			return ErrNoInvitesLeft
		}
		var invitee *uuid.UUID
		u, err := q.InsertUser(ctx, email)
		switch {
		case err == nil:
			invitee = &u.ID
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		inv, err = q.CreateInvite(ctx, db.CreateInviteParams{InviterID: &inviterID, Email: email, InviteeID: invitee})
		if err != nil {
			return err
		}
		quota.Used++
		s.log.Info("invitation sent", "inviter", inviterID, "new_account", invitee != nil)
		return nil
	})
	return inv, quota, err
}

// InviteQuotaOf counts a user's invitations this month.
func (s *Service) InviteQuotaOf(ctx context.Context, userID uuid.UUID, limit int) (InviteQuota, error) {
	used, err := s.q.CountInvitesSince(ctx, db.CountInvitesSinceParams{InviterID: &userID, Since: MonthStart(s.now())})
	return InviteQuota{Used: int(used), Limit: limit}, err
}

// CreateInvited creates the account for email unless it exists, as an admin
// does it: the answer says whether the account is new. With an inviter, a
// new account is recorded as invited by them; nothing is counted.
func (s *Service) CreateInvited(ctx context.Context, inviterID *uuid.UUID, email string) (db.User, bool, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return db.User{}, false, err
	}
	var u db.User
	created := false
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		u, err = q.InsertUser(ctx, email)
		if errors.Is(err, pgx.ErrNoRows) {
			u, err = q.GetUserByEmail(ctx, email)
			return err
		}
		if err != nil {
			return err
		}
		created = true
		if inviterID == nil {
			return nil
		}
		_, err = q.CreateInvite(ctx, db.CreateInviteParams{InviterID: inviterID, Email: email, InviteeID: &u.ID})
		return err
	})
	return u, created, err
}
