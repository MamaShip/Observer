// Package auth implements email login codes and sessions (plan.md §6.1).
// Logging in with an emailed code is also what verifies the address.
// Session tokens are 256-bit random values; only their sha256 is stored.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
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
	CodeTTL           = 10 * time.Minute
	CodesPerDay       = 2 // per address and Beijing calendar day
	ResendAfter       = 60 * time.Second
	codeAttempts      = 5 // guesses per code
	SessionTTL        = 30 * 24 * time.Hour
	sessionTouchEvery = time.Hour
	codesPerIPPerHour = 10
)

// Beijing is where "today" is counted for the daily code limit; users are
// told to try again tomorrow, which should mean after their midnight.
var Beijing = time.FixedZone("UTC+8", 8*60*60)

// CookieName is the session cookie. The __Host- prefix makes browsers
// require Secure, Path=/ and no Domain, so subdomains cannot set or read it.
const CookieName = "__Host-session"

// Errors.
var (
	ErrInvalidEmail = errors.New("invalid email address")
	ErrRateLimited  = errors.New("too many login requests from this address")
	ErrTooSoon      = errors.New("a code was requested less than a minute ago")
	ErrTryTomorrow  = errors.New("no more codes for this email today")
	ErrInvalidCode  = errors.New("login code is wrong, expired or already used")
	ErrDisabled     = errors.New("account disabled")
	ErrNoSession    = errors.New("not logged in")
)

// CodeSender emails login codes.
type CodeSender interface {
	SendLogin(ctx context.Context, to, code string, userID *uuid.UUID, valid time.Duration) error
}

// Config configures the service.
type Config struct {
	Mode string
}

// Service implements login and sessions.
type Service struct {
	pool   *pgxpool.Pool
	q      *db.Queries
	sender CodeSender
	cfg    Config
	log    *slog.Logger
	now    func() time.Time
	ips    *ipLimiter
}

// New builds the service.
func New(pool *pgxpool.Pool, sender CodeSender, cfg Config, log *slog.Logger) *Service {
	return &Service{
		pool: pool, q: db.New(pool), sender: sender, cfg: cfg, log: log.With("component", "auth"), now: time.Now,
		ips: newIPLimiter(rate.Every(time.Hour/codesPerIPPerHour), codesPerIPPerHour),
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

// CodeQuota is what the login page shows after a request.
type CodeQuota struct {
	Remaining int           // codes this address can still request today
	ResendIn  time.Duration // wait before requesting another; 0 if none left
}

// today is the Beijing calendar day of t, as a date value.
func today(t time.Time) time.Time {
	y, m, d := t.In(Beijing).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// RequestCode emails a login code. Every address gets CodesPerDay a day,
// at least ResendAfter apart; past that it returns ErrTryTomorrow or
// ErrTooSoon (with the quota filled in). The limit is counted the same way
// for every address, and the request succeeds silently when no email is sent
// (unknown address in invite mode, disabled account), so the answers do not
// reveal which addresses have accounts.
func (s *Service) RequestCode(ctx context.Context, email string, ip netip.Addr) (CodeQuota, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return CodeQuota{}, err
	}
	now := s.now()
	if ip.IsValid() && !s.ips.allow(ip, now) {
		return CodeQuota{}, ErrRateLimited
	}
	log := s.log.With("email_domain", email[strings.LastIndex(email, "@")+1:])
	day := today(now)

	sent, err := s.q.ClaimLoginRequest(ctx, db.ClaimLoginRequestParams{
		Email: email, Day: day, Now: now, PerDay: CodesPerDay, ResendBefore: now.Add(-ResendAfter),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		r, err := s.q.GetLoginRequest(ctx, db.GetLoginRequestParams{Email: email, Day: day})
		if err != nil {
			return CodeQuota{}, err
		}
		if r.Sent >= CodesPerDay {
			return CodeQuota{}, ErrTryTomorrow
		}
		return CodeQuota{Remaining: CodesPerDay - int(r.Sent), ResendIn: r.LastAt.Add(ResendAfter).Sub(now)}, ErrTooSoon
	}
	if err != nil {
		return CodeQuota{}, err
	}
	quota := CodeQuota{Remaining: CodesPerDay - int(sent)}
	if quota.Remaining > 0 {
		quota.ResendIn = ResendAfter
	}

	var userID *uuid.UUID
	user, err := s.q.GetUserByEmail(ctx, email)
	switch {
	case err == nil:
		if user.Status != "active" {
			log.Info("login code not sent: account disabled")
			return quota, nil
		}
		userID = &user.ID
	case errors.Is(err, pgx.ErrNoRows):
		if s.cfg.Mode != ModeOpen {
			log.Info("login code not sent: not invited")
			return quota, nil
		}
	default:
		return CodeQuota{}, err
	}

	code, err := newCode()
	if err != nil {
		return CodeQuota{}, err
	}
	p := db.CreateLoginCodeParams{Email: email, CodeHash: hashCode(email, code), ExpiresAt: now.Add(CodeTTL)}
	if ip.IsValid() {
		p.RequestIp = &ip
	}
	if err := s.q.CreateLoginCode(ctx, p); err != nil {
		return CodeQuota{}, err
	}
	if err := s.sender.SendLogin(ctx, email, code, userID, CodeTTL); err != nil {
		// Not the user's fault: give the attempt back.
		if rerr := s.q.ReleaseLoginRequest(context.WithoutCancel(ctx), db.ReleaseLoginRequestParams{Email: email, Day: day}); rerr != nil {
			log.Error("could not release login request", "err", rerr)
		}
		return CodeQuota{}, err
	}
	return quota, nil
}

// Session is a newly created session.
type Session struct {
	Token string // give to the client; it is not stored
	User  db.User
	TTL   time.Duration
}

// Verify checks the latest code sent to email and starts a session. Each
// call uses up one of the code's guesses. In open mode the first login
// creates the account.
func (s *Service) Verify(ctx context.Context, email, code, userAgent string) (Session, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return Session{}, ErrInvalidCode
	}
	code = strings.TrimSpace(code)
	if !validCode(code) {
		return Session{}, ErrInvalidCode
	}
	now := s.now()
	c, err := s.q.AttemptLoginCode(ctx, db.AttemptLoginCodeParams{Email: email, Now: now, MaxAttempts: codeAttempts})
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrInvalidCode
	}
	if err != nil {
		return Session{}, err
	}
	if subtle.ConstantTimeCompare(c.CodeHash, hashCode(email, code)) != 1 {
		return Session{}, ErrInvalidCode
	}

	var out Session
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		n, err := q.ConsumeLoginCode(ctx, db.ConsumeLoginCodeParams{ID: c.ID, Now: now})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrInvalidCode // used concurrently
		}
		user, err := q.GetUserByEmail(ctx, email)
		switch {
		case errors.Is(err, pgx.ErrNoRows) && s.cfg.Mode == ModeOpen:
			user, err = q.CreateUser(ctx, email)
		case errors.Is(err, pgx.ErrNoRows):
			return ErrInvalidCode // account removed after the code was sent
		}
		if err != nil {
			return err
		}
		if user.Status != "active" {
			return ErrDisabled
		}
		if err := q.TouchUserLogin(ctx, db.TouchUserLoginParams{ID: user.ID, Now: now}); err != nil {
			return err
		}
		out, err = s.newSession(ctx, q, user, userAgent)
		return err
	})
	return out, err
}

// IssueSession starts a session for an active user without a login code.
// It is for operators (observer admin session), e.g. to smoke-test the
// production site.
func (s *Service) IssueSession(ctx context.Context, email, userAgent string) (Session, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return Session{}, err
	}
	user, err := s.q.GetUserByEmail(ctx, email)
	if err != nil {
		return Session{}, err
	}
	if user.Status != "active" {
		return Session{}, ErrDisabled
	}
	return s.newSession(ctx, s.q, user, userAgent)
}

func (s *Service) newSession(ctx context.Context, q *db.Queries, user db.User, userAgent string) (Session, error) {
	token, hash, err := newToken()
	if err != nil {
		return Session{}, err
	}
	if err := q.CreateSession(ctx, db.CreateSessionParams{
		UserID: user.ID, TokenHash: hash, UserAgent: nonEmpty(truncate(userAgent, 300)), ExpiresAt: s.now().Add(SessionTTL),
	}); err != nil {
		return Session{}, err
	}
	return Session{Token: token, User: user, TTL: SessionTTL}, nil
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

// DeleteAccount removes a user with their sessions and watches, and the
// address from login codes, the email log and invitations. Per-day code counts stay
// until Cleanup (two days), so deleting an account does not reset the limit.
func (s *Service) DeleteAccount(ctx context.Context, userID uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		u, err := q.GetUser(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := q.DeleteLoginCodesFor(ctx, u.Email); err != nil {
			return err
		}
		if err := q.ScrubEmailLog(ctx, db.ScrubEmailLogParams{UserID: &u.ID, Email: u.Email}); err != nil {
			return err
		}
		if err := q.ForgetInvites(ctx, db.ForgetInvitesParams{Email: u.Email, UserID: &u.ID}); err != nil {
			return err
		}
		return q.DeleteUser(ctx, u.ID)
	})
}

// EmailLogRetention is how long sent-email records (with addresses) are kept.
const EmailLogRetention = 90 * 24 * time.Hour

// Cleanup deletes expired login codes, old request counts, sessions and
// email records older than EmailLogRetention.
func (s *Service) Cleanup(ctx context.Context) error {
	now := s.now()
	codes, err := s.q.DeleteExpiredLoginCodes(ctx, now.Add(-24*time.Hour))
	if err != nil {
		return err
	}
	mails, err := s.q.DeleteOldEmailLog(ctx, now.Add(-EmailLogRetention))
	if err != nil {
		return err
	}
	reqs, err := s.q.DeleteOldLoginRequests(ctx, today(now).AddDate(0, 0, -2))
	if err != nil {
		return err
	}
	sess, err := s.q.DeleteExpiredSessions(ctx, now)
	if err == nil && codes+reqs+sess+mails > 0 {
		s.log.Info("expired records cleaned up", "login_codes", codes, "login_requests", reqs, "sessions", sess, "email_log", mails)
	}
	return err
}

// newCode returns a uniformly random 6-digit code.
func newCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("random code: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func validCode(c string) bool {
	if len(c) != 6 {
		return false
	}
	for _, r := range c {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// hashCode is the stored form of a code. Six digits are no secret from
// anyone holding the table; the hash only keeps them out of plain sight
// for the ten minutes a code lives.
func hashCode(email, code string) []byte {
	h := sha256.Sum256([]byte(email + "\x00" + code))
	return h[:]
}

// newToken returns a random session token and its stored hash.
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
