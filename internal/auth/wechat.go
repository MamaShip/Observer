package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MamaShip/Observer/internal/store/db"
)

// WeChat Official Account binding (plan.md §6.7). A WeChat user, known by
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
		t, err := q.UseWechatToken(ctx, db.UseWechatTokenParams{TokenHash: hash, Purpose: wechatBind, Now: s.now()})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrWechatLink
		}
		if err != nil {
			return err
		}
		// The user first (as everywhere else), then the OpenID, which may
		// have no row yet.
		u, err := q.LockUser(ctx, userID)
		if err != nil {
			return err
		}
		if u.Status != "active" {
			return ErrDisabled
		}
		if err := q.LockWechatOpenID(ctx, t.Openid); err != nil {
			return err
		}
		// Links sent for the old pairs would sign in the wrong account.
		if err := q.DeleteWechatLoginTokens(ctx, db.DeleteWechatLoginTokensParams{Openid: t.Openid, UserID: &userID}); err != nil {
			return err
		}
		if err := q.DeleteWechatBindingsFor(ctx, db.DeleteWechatBindingsForParams{Openid: t.Openid, UserID: userID}); err != nil {
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
		t, err := q.UseWechatToken(ctx, db.UseWechatTokenParams{TokenHash: hash, Purpose: wechatLogin, Now: s.now()})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrWechatLink
		}
		if err != nil {
			return err
		}
		u, err := q.LockUser(ctx, *t.UserID)
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

// ForgetWechat removes openid's binding and links: the WeChat user sent
// 解绑 or unfollowed the account. It reports whether there was a binding.
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
