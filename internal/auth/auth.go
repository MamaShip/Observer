// Package auth implements email magic-link login and sessions (plan.md §6.1).
// Tokens are 256-bit random values; only their sha256 is stored.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"

	"github.com/MamaShip/Observer/internal/store/db"
)

// Registration modes.
const (
	ModeOpen   = "open"   // anyone can sign up by logging in
	ModeInvite = "invite" // only users an admin created can log in
)

// Lifetimes and limits.
const (
	LoginTokenTTL     = 15 * time.Minute
	SessionTTL        = 30 * 24 * time.Hour
	sessionTouchEvery = time.Hour
	linksPerEmail     = 3 // per linksWindow
	linksWindow       = 10 * time.Minute
	linksPerIPPerHour = 10
)

// CookieName is the session cookie. The __Host- prefix makes browsers
// require Secure, Path=/ and no Domain, so subdomains cannot set or read it.
const CookieName = "__Host-session"

// Errors.
var (
	ErrInvalidEmail = errors.New("invalid email address")
	ErrRateLimited  = errors.New("too many login requests")
	ErrInvalidToken = errors.New("login link is invalid, expired or already used")
	ErrDisabled     = errors.New("account disabled")
	ErrNoSession    = errors.New("not logged in")
)

// LinkSender emails login links.
type LinkSender interface {
	SendLogin(ctx context.Context, to, link string, userID *uuid.UUID, valid time.Duration) error
}

// Config configures the service.
type Config struct {
	BaseURL string // login links point to BaseURL + "/login/verify#token=..."
	Mode    string
}

// Service implements login and sessions.
type Service struct {
	pool   *pgxpool.Pool
	q      *db.Queries
	sender LinkSender
	cfg    Config
	log    *slog.Logger
	now    func() time.Time
	ips    *ipLimiter
}

// New builds the service.
func New(pool *pgxpool.Pool, sender LinkSender, cfg Config, log *slog.Logger) *Service {
	return &Service{
		pool: pool, q: db.New(pool), sender: sender, cfg: cfg, log: log.With("component", "auth"), now: time.Now,
		ips: newIPLimiter(rate.Every(time.Hour/linksPerIPPerHour), linksPerIPPerHour),
	}
}

// NormalizeEmail validates an address and returns its canonical form.
func NormalizeEmail(s string) (string, error) {
	s = strings.TrimSpace(s)
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || a.Name != "" || len(s) > 254 || !strings.Contains(s[strings.LastIndex(s, "@"):], ".") {
		return "", ErrInvalidEmail
	}
	return strings.ToLower(s), nil
}

// RequestLink emails a login link. To avoid revealing which addresses have
// accounts, it succeeds silently when no email is sent (unknown address in
// invite mode, disabled account, or too many links for this address).
func (s *Service) RequestLink(ctx context.Context, email string, ip netip.Addr) error {
	email, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	if ip.IsValid() && !s.ips.allow(ip, s.now()) {
		return ErrRateLimited
	}
	log := s.log.With("email_domain", email[strings.LastIndex(email, "@")+1:])

	var userID *uuid.UUID
	user, err := s.q.GetUserByEmail(ctx, email)
	switch {
	case err == nil:
		if user.Status != "active" {
			log.Info("login link not sent: account disabled")
			return nil
		}
		userID = &user.ID
	case errors.Is(err, pgx.ErrNoRows):
		if s.cfg.Mode != ModeOpen {
			log.Info("login link not sent: not invited")
			return nil
		}
	default:
		return err
	}

	recent, err := s.q.CountRecentLoginTokens(ctx, db.CountRecentLoginTokensParams{Email: email, Since: s.now().Add(-linksWindow)})
	if err != nil {
		return err
	}
	if recent >= linksPerEmail {
		log.Info("login link not sent: too many recent links for this address")
		return nil
	}

	token, hash, err := newToken()
	if err != nil {
		return err
	}
	p := db.CreateLoginTokenParams{Email: email, TokenHash: hash, ExpiresAt: s.now().Add(LoginTokenTTL)}
	if ip.IsValid() {
		p.RequestIp = &ip
	}
	if err := s.q.CreateLoginToken(ctx, p); err != nil {
		return err
	}
	link := s.cfg.BaseURL + "/login/verify#token=" + token
	return s.sender.SendLogin(ctx, email, link, userID, LoginTokenTTL)
}

// Session is a newly created session.
type Session struct {
	Token string // give to the client; it is not stored
	User  db.User
	TTL   time.Duration
}

// Verify consumes a login token and starts a session. In open mode the
// first login creates the account.
func (s *Service) Verify(ctx context.Context, token, userAgent string) (Session, error) {
	hash, ok := hashToken(token)
	if !ok {
		return Session{}, ErrInvalidToken
	}
	var out Session
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		now := s.now()
		email, err := q.ConsumeLoginToken(ctx, db.ConsumeLoginTokenParams{TokenHash: hash, Now: now})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidToken
		}
		if err != nil {
			return err
		}
		user, err := q.GetUserByEmail(ctx, email)
		switch {
		case errors.Is(err, pgx.ErrNoRows) && s.cfg.Mode == ModeOpen:
			user, err = q.CreateUser(ctx, email)
		case errors.Is(err, pgx.ErrNoRows):
			return ErrInvalidToken // account removed after the link was sent
		}
		if err != nil {
			return err
		}
		if user.Status != "active" {
			return ErrDisabled
		}
		sessToken, sessHash, err := newToken()
		if err != nil {
			return err
		}
		if err := q.CreateSession(ctx, db.CreateSessionParams{
			UserID: user.ID, TokenHash: sessHash, UserAgent: nonEmpty(truncate(userAgent, 300)), ExpiresAt: now.Add(SessionTTL),
		}); err != nil {
			return err
		}
		if err := q.TouchUserLogin(ctx, db.TouchUserLoginParams{ID: user.ID, Now: now}); err != nil {
			return err
		}
		out = Session{Token: sessToken, User: user, TTL: SessionTTL}
		return nil
	})
	return out, err
}

// Authenticate returns the user of a session token, extending the session
// (sliding expiry) at most once an hour.
func (s *Service) Authenticate(ctx context.Context, token string) (db.User, error) {
	hash, ok := hashToken(token)
	if !ok {
		return db.User{}, ErrNoSession
	}
	now := s.now()
	row, err := s.q.GetSession(ctx, db.GetSessionParams{TokenHash: hash, Now: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.User{}, ErrNoSession
	}
	if err != nil {
		return db.User{}, err
	}
	if row.User.Status != "active" {
		return db.User{}, ErrDisabled
	}
	if now.Sub(row.Session.LastSeenAt) > sessionTouchEvery {
		if err := s.q.TouchSession(ctx, db.TouchSessionParams{ID: row.Session.ID, Now: now, ExpiresAt: now.Add(SessionTTL)}); err != nil {
			s.log.Warn("could not extend session", "err", err)
		}
	}
	return row.User, nil
}

// Logout ends one session.
func (s *Service) Logout(ctx context.Context, token string) error {
	hash, ok := hashToken(token)
	if !ok {
		return nil
	}
	return s.q.DeleteSession(ctx, hash)
}

// LogoutAll ends every session of a user.
func (s *Service) LogoutAll(ctx context.Context, userID uuid.UUID) (int64, error) {
	return s.q.DeleteUserSessions(ctx, userID)
}

// Cleanup deletes expired login tokens and sessions.
func (s *Service) Cleanup(ctx context.Context) error {
	now := s.now()
	t, err := s.q.DeleteExpiredLoginTokens(ctx, now.Add(-24*time.Hour))
	if err != nil {
		return err
	}
	sess, err := s.q.DeleteExpiredSessions(ctx, now)
	if err == nil && t+sess > 0 {
		s.log.Info("expired logins cleaned up", "login_tokens", t, "sessions", sess)
	}
	return err
}

// newToken returns a random token and its stored hash.
func newToken() (string, []byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("random token: %w", err)
	}
	t := base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(t))
	return t, h[:], nil
}

// hashToken hashes a client-supplied token, rejecting anything that is not
// the shape newToken produces.
func hashToken(t string) ([]byte, bool) {
	if len(t) != 43 {
		return nil, false
	}
	if _, err := base64.RawURLEncoding.DecodeString(t); err != nil {
		return nil, false
	}
	h := sha256.Sum256([]byte(t))
	return h[:], true
}

// ipLimiter is a per-address token bucket, forgotten after an hour idle.
type ipLimiter struct {
	mu      sync.Mutex
	every   rate.Limit
	burst   int
	buckets map[netip.Addr]*ipBucket
}

type ipBucket struct {
	lim  *rate.Limiter
	seen time.Time
}

func newIPLimiter(every rate.Limit, burst int) *ipLimiter {
	return &ipLimiter{every: every, burst: burst, buckets: map[netip.Addr]*ipBucket{}}
}

func (l *ipLimiter) allow(ip netip.Addr, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buckets) > 10000 {
		for k, b := range l.buckets {
			if now.Sub(b.seen) > time.Hour {
				delete(l.buckets, k)
			}
		}
	}
	b := l.buckets[ip]
	if b == nil {
		b = &ipBucket{lim: rate.NewLimiter(l.every, l.burst)}
		l.buckets[ip] = b
	}
	b.seen = now
	return b.lim.AllowN(now, 1)
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}
