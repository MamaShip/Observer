package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/MamaShip/Observer/internal/mail"
	"github.com/MamaShip/Observer/internal/store/db"
)

// InviteLinkTTL starts when the worker prepares the email.
const InviteLinkTTL = 7 * 24 * time.Hour

// InviteMailArgs contains only a database ID. The worker generates the
// credential just before sending it, keeping it out of the job history.
type InviteMailArgs struct {
	InviteID uuid.UUID `json:"invite_id"`
}

func (InviteMailArgs) Kind() string { return "email_invite" }
func (InviteMailArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: mail.Queue, MaxAttempts: 8}
}

// JobInserter queues invitation emails in the caller's transaction.
type JobInserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// SetJobs connects the transactional invitation queue before accepting requests.
func (s *Service) SetJobs(j JobInserter) { s.jobs = j }

// Errors from Invite.
var (
	ErrInviteLink    = errors.New("invitation link is no longer valid")
	ErrInviteSelf    = errors.New("that is the inviter's own address")
	ErrNoInvitesLeft = errors.New("no invitations left this month")
)

// Errors from RevokeInvite.
var (
	ErrInviteNotFound = errors.New("no such invitation")
	ErrInviteAccepted = errors.New("the invited person has already logged in")
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

// Invitation includes the same acceptance status as the inviter's list.
type Invitation struct {
	db.Invite
	Accepted bool
}

// Invite lets email log in, on behalf of the user inviterID, who may send
// limit invitations per Beijing month (0: no limit). Every invitation counts,
// whether or not the address already had an account, and the result is the
// same either way; only repeating one of the user's own invitations is free.
// An existing account is never changed.
func (s *Service) Invite(ctx context.Context, inviterID uuid.UUID, email string, limit int) (Invitation, InviteQuota, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return Invitation{}, InviteQuota{}, err
	}
	var inv Invitation
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
		used, err := q.CountInvitesSince(ctx, db.CountInvitesSinceParams{InviterID: inviterID, Since: MonthStart(s.now())})
		if err != nil {
			return err
		}
		quota.Used = int(used)
		prior, err := q.GetInviteByInviter(ctx, db.GetInviteByInviterParams{InviterID: &inviterID, Email: email})
		if err == nil {
			inv = Invitation{Invite: prior.Invite, Accepted: prior.Accepted}
			return s.queueInvite(ctx, tx, inv.ID) // includes invitations from before email delivery
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if limit > 0 && quota.Used >= limit {
			return ErrNoInvitesLeft
		}
		var invitee *uuid.UUID
		u, err := q.ReserveInvitedAccount(ctx, email)
		if err != nil {
			return err
		}
		if u.Created {
			invitee = &u.ID
		}
		inv.Invite, err = q.CreateInvite(ctx, db.CreateInviteParams{InviterID: &inviterID, Email: email, InviteeID: invitee})
		if err != nil {
			return err
		}
		quota.Used++
		return s.queueInvite(ctx, tx, inv.ID)
	})
	return inv, quota, err
}

// RevokeInvite withdraws one of the user inviterID's invitations that has not
// been accepted. Its place in this month's allowance is given back only if it
// never queued an email, which does not depend on the address. The
// account it created goes too, unless another invitation of the address is
// waiting: that invitation then holds the account. An invitation to an
// address that already had an account is simply removed, so the answer is the
// same either way.
func (s *Service) RevokeInvite(ctx context.Context, inviterID, inviteID uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.LockUser(ctx, inviterID); err != nil {
			return err
		}
		email, err := q.GetOwnedInviteEmail(ctx, db.GetOwnedInviteEmailParams{ID: inviteID, InviterID: &inviterID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInviteNotFound
		}
		if err != nil {
			return err
		}
		// Lock the account before changing any invitation, including one
		// that may inherit this account from another inviter.
		invitee, err := q.GetUserByEmailForUpdate(ctx, email)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInviteNotFound
		}
		if err != nil {
			return err
		}
		inv, err := q.GetInviteForUpdate(ctx, db.GetInviteForUpdateParams{ID: inviteID, InviterID: &inviterID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInviteNotFound
		}
		if err != nil {
			return err
		}
		if inv.InviteeID != nil && invitee.LastLoginAt != nil {
			return ErrInviteAccepted
		}
		if err := q.DeleteInvite(ctx, inv.ID); err != nil {
			return err
		}
		if inv.MailQueued {
			// Keep it counted, or inviting and revoking in turn would email
			// any number of addresses.
			if err := q.RecordRevokedInvite(ctx, db.RecordRevokedInviteParams{InviterID: inviterID, InvitedAt: inv.CreatedAt}); err != nil {
				return err
			}
		}
		if inv.InviteeID != nil {
			passed, err := q.PassInvitee(ctx, db.PassInviteeParams{InviteeID: inv.InviteeID, Email: inv.Email})
			if err != nil {
				return err
			}
			if passed == 0 {
				if _, err := q.DeleteUnusedAccount(ctx, *inv.InviteeID); err != nil {
					return err
				}
			}
		}
		s.log.Info("invitation revoked", "inviter", inviterID, "account_created", inv.InviteeID != nil)
		return nil
	})
}

// InviteQuotaOf counts a user's invitations this month.
func (s *Service) InviteQuotaOf(ctx context.Context, userID uuid.UUID, limit int) (InviteQuota, error) {
	used, err := s.q.CountInvitesSince(ctx, db.CountInvitesSinceParams{InviterID: userID, Since: MonthStart(s.now())})
	return InviteQuota{Used: int(used), Limit: limit}, err
}

// InviteFromCLI provisions an address without attributing it to a user.
func (s *Service) InviteFromCLI(ctx context.Context, email string) error {
	email, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.CreateUser(ctx, email); err != nil {
			return err
		}
		prior, err := q.GetInviteByInviter(ctx, db.GetInviteByInviterParams{Email: email})
		if err == nil {
			return s.queueInvite(ctx, tx, prior.Invite.ID)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		inv, err := q.CreateInvite(ctx, db.CreateInviteParams{Email: email})
		if err != nil {
			return err
		}
		return s.queueInvite(ctx, tx, inv.ID)
	})
}

// queueInvite queues an invitation's first email, or a fresh link when the
// invitation is repeated after its last link expired unused.
func (s *Service) queueInvite(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	q := s.q.WithTx(tx)
	n, err := q.QueueInviteMailOnce(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		n, err = q.RequeueExpiredInviteMail(ctx, db.RequeueExpiredInviteMailParams{ID: id, Now: s.now()})
		if err != nil || n == 0 {
			return err
		}
	}
	_, err = s.jobs.InsertTx(ctx, tx, InviteMailArgs{InviteID: id}, nil)
	return err
}

// DeliverInvite prepares a single-use credential for an active account's first login.
// Revocation deletes the invitation, so even a message already in transit is harmless.
func (s *Service) DeliverInvite(ctx context.Context, id uuid.UUID) error {
	token, hash, err := newToken()
	if err != nil {
		return err
	}
	email, err := s.q.PrepareInviteMail(ctx, db.PrepareInviteMailParams{
		ID: id, TokenHash: hash, ExpiresAt: s.now().Add(InviteLinkTTL),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.sender.SendInvite(ctx, email, token, InviteLinkTTL); err != nil {
		return err
	}
	// A retry would replace the token in the email just sent, so record
	// the delivery even if the job's context ends now.
	return s.q.MarkInviteMailed(context.WithoutCancel(ctx), id)
}

// AcceptInvite uses proof of mailbox ownership to complete registration. A
// concurrent first login (by code or another invitation) invalidates this link.
func (s *Service) AcceptInvite(ctx context.Context, token, userAgent string) (Session, error) {
	hash, ok := hashToken(token)
	if !ok {
		return Session{}, ErrInviteLink
	}
	var sess Session
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		inv, err := q.GetInviteByToken(ctx, hash)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInviteLink
		}
		if err != nil {
			return err
		}
		u, err := q.GetUserByEmailForUpdate(ctx, inv.Email)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInviteLink
		}
		if err != nil {
			return err
		}
		if u.Status != "active" || u.LastLoginAt != nil {
			return ErrInviteLink
		}
		now := s.now()
		n, err := q.ConsumeInviteToken(ctx, db.ConsumeInviteTokenParams{ID: inv.ID, TokenHash: hash, Now: now})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrInviteLink
		}
		if err := q.TouchUserLogin(ctx, db.TouchUserLoginParams{ID: u.ID, Now: now}); err != nil {
			return err
		}
		sess, err = s.newSession(ctx, q, u, userAgent)
		return err
	})
	return sess, err
}
