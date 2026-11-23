package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MamaShip/Observer/internal/store/db"
)

// WeChat Official Account binding (docs/design.md §6.7). A WeChat user, known by
// the OpenID our account sees, is bound to one user and a user to one WeChat
// user. Replies carry one-time links: a bind link lets whoever is logged in
// on the website take the binding (and submit the article the WeChat user
// sent before binding); a login link signs the bound user in.

// Lifetimes of the links sent in replies.
const (
	WechatBindTTL  = 30 * time.Minute
	WechatLoginTTL = 24 * time.Hour
)

// Token purposes (wechat_tokens.purpose).
const (
	wechatBind  = "bind"
	wechatLogin = "login"
)

// ErrWechatLink means a link token is unknown, already used or expired, or
// its binding has changed since it was sent.
var ErrWechatLink = errors.New("wechat link is invalid or expired")

// WechatBinding is a bound WeChat user.
type WechatBinding struct {
	OpenID string
	User   db.User
	Since  time.Time
}

// WechatBindingFor returns the user bound to openid; ok is false if none.
func (s *Service) WechatBindingFor(ctx context.Context, openid string) (b WechatBinding, ok bool, err error) {
	r, err := s.q.GetWechatBindingByOpenID(ctx, openid)
	if errors.Is(err, pgx.ErrNoRows) {
		return WechatBinding{}, false, nil
	}
	if err != nil {
		return WechatBinding{}, false, err
	}
	return WechatBinding{OpenID: r.WechatBinding.Openid, User: r.User, Since: r.WechatBinding.CreatedAt}, true, nil
}

// WechatBoundSince returns when the user bound WeChat, or nil if not bound.
func (s *Service) WechatBoundSince(ctx context.Context, userID uuid.UUID) (*time.Time, error) {
	b, err := s.q.GetWechatBindingByUser(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b.CreatedAt, nil
}

// NewWechatBindLink makes a bind token for openid. pendingURL, if not
// empty, is an article link to start watching once bound.
func (s *Service) NewWechatBindLink(ctx context.Context, openid, pendingURL string) (string, error) {
	return s.newWechatToken(ctx, db.CreateWechatTokenParams{
		Purpose: wechatBind, Openid: openid, PendingUrl: nonEmpty(truncate(pendingURL, 2048)),
		ExpiresAt: s.now().Add(WechatBindTTL),
	})
}

// NewWechatLoginLink makes a login token for the user bound to openid.
func (s *Service) NewWechatLoginLink(ctx context.Context, openid string, userID uuid.UUID) (string, error) {
	return s.newWechatToken(ctx, db.CreateWechatTokenParams{
		Purpose: wechatLogin, Openid: openid, UserID: &userID, ExpiresAt: s.now().Add(WechatLoginTTL),
	})
}

func (s *Service) newWechatToken(ctx context.Context, p db.CreateWechatTokenParams) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}
	p.TokenHash = hash
	return token, s.q.CreateWechatToken(ctx, p)
}

// BindWechat uses a bind token to bind its WeChat user to userID, replacing
// whatever either side was bound to before. It returns the article link the
// WeChat user sent before binding, if any.
func (s *Service) BindWechat(ctx context.Context, userID uuid.UUID, token string) (pendingURL string, err error) {
	hash, ok := hashToken(token)
	if !ok {
		return "", ErrWechatLink
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		t, u, err := s.useWechatToken(ctx, q, hash, wechatBind, &userID)
		if err != nil {
			return err
		}
		if u.Status != "active" {
			return ErrDisabled
		}
		// Bindings before tokens, as in ForgetWechat. Links sent for the
		// old pairs would sign in the wrong account.
		if err := q.DeleteWechatBindingsFor(ctx, db.DeleteWechatBindingsForParams{Openid: t.Openid, UserID: userID}); err != nil {
			return err
		}
		if err := q.DeleteWechatLoginTokens(ctx, db.DeleteWechatLoginTokensParams{Openid: t.Openid, UserID: &userID}); err != nil {
			return err
		}
		if _, err := q.CreateWechatBinding(ctx, db.CreateWechatBindingParams{Openid: t.Openid, UserID: userID}); err != nil {
			return err
		}
		if t.PendingUrl != nil {
			pendingURL = *t.PendingUrl
		}
		return nil
	})
	return pendingURL, err
}

// WechatLogin uses a login token to start a session for the bound user. The
// binding must still be the one the link was sent for.
func (s *Service) WechatLogin(ctx context.Context, token, userAgent string) (Session, error) {
	hash, ok := hashToken(token)
	if !ok {
		return Session{}, ErrWechatLink
	}
	var out Session
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		t, u, err := s.useWechatToken(ctx, q, hash, wechatLogin, nil)
		if err != nil {
			return err
		}
		if _, err := q.GetWechatBindingForUpdate(ctx, db.GetWechatBindingForUpdateParams{Openid: t.Openid, UserID: u.ID}); errors.Is(err, pgx.ErrNoRows) {
			return ErrWechatLink
		} else if err != nil {
			return err
		}
		if u.Status != "active" {
			return ErrDisabled
		}
		if err := q.TouchUserLogin(ctx, db.TouchUserLoginParams{ID: u.ID, Now: s.now()}); err != nil {
			return err
		}
		out, err = s.newSession(ctx, q, u, userAgent)
		return err
	})
	return out, err
}

// UnbindWechat removes the user's binding (from the website).
func (s *Service) UnbindWechat(ctx context.Context, userID uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		// The lock order of useWechatToken. With the user locked, the
		// binding can only go away (ForgetWechat), not change.
		if _, err := q.LockUser(ctx, userID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		b, err := q.GetWechatBindingByUser(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := q.LockWechatOpenID(ctx, b.Openid); err != nil {
			return err
		}
		openid, err := q.DeleteWechatBindingByUser(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return q.DeleteWechatTokensByOpenID(ctx, openid)
	})
}

// useWechatToken uses a token inside a transaction. Every change to
// bindings and links locks in the same order, so concurrent binds, logins
// and unbinds wait for each other instead of deadlocking: the user row,
// then the OpenID, then binding rows, then token rows. The token is read
// first without a lock to learn its user and OpenID, and used once both are
// locked. userID is the user to lock (the one binding), or nil for the
// token's own user (a login link).
func (s *Service) useWechatToken(ctx context.Context, q *db.Queries, hash []byte, purpose string, userID *uuid.UUID) (db.WechatToken, db.User, error) {
	now := s.now()
	t, err := q.PeekWechatToken(ctx, db.PeekWechatTokenParams{TokenHash: hash, Purpose: purpose, Now: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.WechatToken{}, db.User{}, ErrWechatLink
	}
	if err != nil {
		return db.WechatToken{}, db.User{}, err
	}
	if userID == nil {
		userID = t.UserID
	}
	if userID == nil {
		return db.WechatToken{}, db.User{}, ErrWechatLink
	}
	u, err := q.LockUser(ctx, *userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.WechatToken{}, db.User{}, ErrWechatLink // deleted meanwhile
	}
	if err != nil {
		return db.WechatToken{}, db.User{}, err
	}
	// The OpenID may have no row yet: an advisory lock.
	if err := q.LockWechatOpenID(ctx, t.Openid); err != nil {
		return db.WechatToken{}, db.User{}, err
	}
	t, err = q.UseWechatToken(ctx, db.UseWechatTokenParams{TokenHash: hash, Purpose: purpose, Now: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.WechatToken{}, db.User{}, ErrWechatLink // used or removed meanwhile
	}
	if err != nil {
		return db.WechatToken{}, db.User{}, err
	}
	return t, u, nil
}

// ForgetWechat removes openid's binding and links: the WeChat user sent
// 解绑 or unfollowed the account. It reports whether there was a binding.
// It locks no user, so it starts at the OpenID in useWechatToken's order.
func (s *Service) ForgetWechat(ctx context.Context, openid string) (bool, error) {
	var n int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockWechatOpenID(ctx, openid); err != nil {
			return err
		}
		var err error
		if n, err = q.DeleteWechatBindingByOpenID(ctx, openid); err != nil {
			return err
		}
		return q.DeleteWechatTokensByOpenID(ctx, openid)
	})
	return n > 0, err
}
