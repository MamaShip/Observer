package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MamaShip/Observer/internal/store/db"
)

// Public invitation links (plan.md §6.1): an admin opens a link for a
// number of new accounts and a period. Holders still prove their address
// with a login code; the link only lets an unknown address receive one and
// create its account. A place is used only when an account is created.

// Limits of a link's settings.
const (
	LinkMaxUses  = 100
	LinkMinValid = time.Hour
	LinkMaxValid = 30 * 24 * time.Hour
	LinkNoteMax  = 100 // characters
)

// Link states, as the admin console shows them.
const (
	LinkActive  = "active"
	LinkNoCodes = "no_codes" // still open for codes already sent, but sends no more
	LinkFull    = "full"
	LinkExpired = "expired"
	LinkRevoked = "revoked"
)

// Errors from CreateInviteLink.
var (
	ErrRegistrationOpen = errors.New("registration is open; invitations are not needed")
	ErrLinkSettings     = errors.New("invitation link settings out of range")
)

// ErrLinkCodes means a link that is still open has sent all the login
// codes it may: codes already sent still sign up, but no new ones go out.
var ErrLinkCodes = errors.New("invitation link has sent all its login codes")

// linkCodeCap is how many login codes requests through a link may send,
// whichever addresses they are for: enough for everyone invited to retry,
// little enough that a leaked link cannot use up the day's emails.
func linkCodeCap(maxUses int32) int32 { return max(3*maxUses, 10) }

// deriveLinkKey keeps link tokens apart from other uses of the signing key.
func deriveLinkKey(secret []byte) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("observer invite links"))
	return m.Sum(nil)
}

func (s *Service) linkMAC(id uuid.UUID) []byte {
	m := hmac.New(sha256.New, s.linkKey)
	m.Write([]byte("invite-link\x00"))
	m.Write(id[:])
	return m.Sum(nil)[:16]
}

// LinkToken is the credential in a link's URL: the id and its MAC, so the
// admin console can show the link again without anything secret stored.
func (s *Service) LinkToken(id uuid.UUID) string {
	b := make([]byte, 0, 32)
	b = append(b, id[:]...)
	b = append(b, s.linkMAC(id)...)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Service) parseLinkToken(t string) (uuid.UUID, bool) {
	if len(t) != 43 {
		return uuid.Nil, false
	}
	b, err := base64.RawURLEncoding.DecodeString(t)
	if err != nil || len(b) != 32 {
		return uuid.Nil, false
	}
	id, err := uuid.FromBytes(b[:16])
	if err != nil || !hmac.Equal(b[16:], s.linkMAC(id)) {
		return uuid.Nil, false
	}
	return id, true
}

// LinkStatus says whether a link still accepts sign-ups, or why not.
func LinkStatus(l db.InviteLink, now time.Time) string {
	switch {
	case l.RevokedAt != nil:
		return LinkRevoked
	case !now.Before(l.ExpiresAt):
		return LinkExpired
	case l.Used >= l.MaxUses:
		return LinkFull
	}
	return LinkActive
}

// LinkAdminStatus is LinkStatus as the admin console shows it: an open
// link that can send no more codes is told apart. Sign-ups go by LinkStatus,
// since codes sent before the cap still work.
func LinkAdminStatus(l db.InviteLink, now time.Time) string {
	st := LinkStatus(l, now)
	if st == LinkActive && l.CodesSent >= linkCodeCap(l.MaxUses) {
		return LinkNoCodes
	}
	return st
}

// openLink returns the link a token names if it still accepts sign-ups;
// otherwise ErrInviteLink, whatever the reason.
func (s *Service) openLink(ctx context.Context, q *db.Queries, token string, lock bool) (db.InviteLink, error) {
	id, ok := s.parseLinkToken(token)
	if !ok {
		return db.InviteLink{}, ErrInviteLink
	}
	get := q.GetInviteLink
	if lock {
		get = q.LockInviteLink
	}
	l, err := get(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.InviteLink{}, ErrInviteLink
	}
	if err != nil {
		return db.InviteLink{}, err
	}
	if LinkStatus(l, s.now()) != LinkActive {
		return db.InviteLink{}, ErrInviteLink
	}
	return l, nil
}

// CreateInviteLink opens a link for maxUses new accounts during valid.
// creatorID is nil from the command line.
func (s *Service) CreateInviteLink(ctx context.Context, creatorID *uuid.UUID, maxUses int, valid time.Duration, note string) (db.InviteLink, error) {
	if s.cfg.Mode != ModeInvite {
		return db.InviteLink{}, ErrRegistrationOpen
	}
	note = strings.TrimSpace(note)
	if maxUses < 1 || maxUses > LinkMaxUses || valid < LinkMinValid || valid > LinkMaxValid ||
		!utf8.ValidString(note) || utf8.RuneCountInString(note) > LinkNoteMax {
		return db.InviteLink{}, ErrLinkSettings
	}
	l, err := s.q.CreateInviteLink(ctx, db.CreateInviteLinkParams{
		CreatorID: creatorID, Note: note, MaxUses: int32(maxUses), ExpiresAt: s.now().Add(valid),
	})
	if err == nil {
		s.log.Info("invitation link created", "link", l.ID, "max_uses", maxUses, "valid", valid)
	}
	return l, err
}

// RevokeInviteLink closes a link at once. The accounts it created stay.
func (s *Service) RevokeInviteLink(ctx context.Context, id uuid.UUID) error {
	n, err := s.q.RevokeInviteLink(ctx, db.RevokeInviteLinkParams{ID: id, Now: s.now()})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrInviteNotFound
	}
	s.log.Info("invitation link revoked", "link", id)
	return nil
}

// CheckInviteLink tells the sign-up page until when a link is open.
func (s *Service) CheckInviteLink(ctx context.Context, token string) (time.Time, error) {
	if s.cfg.Mode != ModeInvite {
		return time.Time{}, ErrRegistrationOpen
	}
	l, err := s.openLink(ctx, s.q, token, false)
	return l.ExpiresAt, err
}

// signUpThroughLink creates email's account on one of the link's places,
// inside Verify's transaction. When the address got an account meanwhile
// (another invitation), that account logs in and no place is used.
func (s *Service) signUpThroughLink(ctx context.Context, q *db.Queries, email, token string) (db.User, error) {
	l, err := s.openLink(ctx, q, token, true)
	if err != nil {
		return db.User{}, err
	}
	user, err := q.CreateUserFromLink(ctx, db.CreateUserFromLinkParams{Email: email, InviteLinkID: &l.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return q.GetUserByEmailForUpdate(ctx, email)
	}
	if err != nil {
		return db.User{}, err
	}
	if err := q.UseInviteLink(ctx, l.ID); err != nil {
		return db.User{}, err
	}
	s.log.Info("account created through invitation link", "link", l.ID)
	return user, nil
}

// closedLinkError tells why a link refused to count a code it was asked
// for (it closed meanwhile, or reached its cap): ErrInviteLink or
// ErrLinkCodes.
func (s *Service) closedLinkError(ctx context.Context, id uuid.UUID) error {
	l, err := s.q.GetInviteLink(ctx, id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil && LinkStatus(l, s.now()) == LinkActive {
		return ErrLinkCodes
	}
	return ErrInviteLink
}
