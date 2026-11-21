// Package watch is the business core: submitting links, the first fetch,
// periodic checks, snapshots and the end of watches (plan.md §5–6). It
// talks to WeChat through Session and queues work through JobInserter, so
// tests can replace both.
package watch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MamaShip/Observer/internal/blob"
	"github.com/MamaShip/Observer/internal/store/db"
	"github.com/MamaShip/Observer/internal/wechat"
)

// Session fetches from WeChat; *wechat.Session implements it. One session
// is used per job, so cookies and the User-Agent stay consistent within it.
type Session interface {
	FetchPage(ctx context.Context, url string, mode wechat.FetchMode) (*wechat.Page, error)
	FetchAsset(ctx context.Context, url, referer string) (*wechat.Asset, error)
	Close()
}

// Alerter notifies the operators; notify.Notifier implements it.
type Alerter interface {
	Alert(ctx context.Context, key, subject, body string)
}

type logAlerter struct{ log *slog.Logger }

func (l logAlerter) Alert(_ context.Context, key, subject, body string) {
	l.log.Error("alert", "key", key, "subject", subject, "body", body)
}

// Config holds the tunables.
type Config struct {
	WatchDays        int
	MinFetchInterval time.Duration
	CanaryAliveURL   string
	CanaryDeletedURL string
}

// Deps are the service's collaborators.
type Deps struct {
	Pool       *pgxpool.Pool
	Blob       blob.Store
	NewSession func() Session
	Log        *slog.Logger
	Config     Config
	Alerter    Alerter // defaults to logging
}

// Service implements the watch workflows.
type Service struct {
	pool       *pgxpool.Pool
	q          *db.Queries
	blob       blob.Store
	newSession func() Session
	log        *slog.Logger
	cfg        Config
	jobs       JobInserter
	alert      Alerter
	limiter    *Limiter
	breaker    *Breaker
	now        func() time.Time
}

// New builds the service. Call SetJobs before running any workflow.
func New(d Deps) *Service {
	if d.Alerter == nil {
		d.Alerter = logAlerter{d.Log}
	}
	return &Service{
		alert:      d.Alerter,
		pool:       d.Pool,
		q:          db.New(d.Pool),
		blob:       d.Blob,
		newSession: d.NewSession,
		log:        d.Log.With("component", "watch"),
		cfg:        d.Config,
		limiter:    NewLimiter(d.Config.MinFetchInterval),
		breaker:    NewBreaker(3, 10*time.Minute, 30*time.Minute),
		now:        time.Now,
	}
}

// SetJobs sets the job queue. It is separate from New because the River
// client is built from workers that already hold the service.
func (s *Service) SetJobs(j JobInserter) { s.jobs = j }

// BreakerOpenUntil reports when fetching resumes, or zero if it is running.
func (s *Service) BreakerOpenUntil() time.Time { return s.breaker.OpenUntil() }

// Errors returned by Submit and Renew.
var (
	ErrBadLink = errors.New("not a supported WeChat article link")
	// ErrOpenLimit: the user already has Quota.MaxOpen open watches.
	ErrOpenLimit = errors.New("too many open watches")
	// ErrDailyLimit: the user created Quota.MaxDaily watches in the last 24 hours.
	ErrDailyLimit = errors.New("too many watches created today")
)

// Quota limits one user's watches (plan.md §18). Zero means no limit.
type Quota struct {
	MaxOpen  int // resolving or active watches
	MaxDaily int // watches created in the last 24 hours
}

// checkOpen returns ErrOpenLimit if the user may not open another watch.
// The caller holds the user's row lock, so concurrent requests are counted
// one after the other.
func checkOpen(ctx context.Context, q *db.Queries, userID uuid.UUID, quota Quota) error {
	if quota.MaxOpen <= 0 {
		return nil
	}
	n, err := q.CountOpenWatches(ctx, userID)
	if err != nil {
		return err
	}
	if n >= int64(quota.MaxOpen) {
		return ErrOpenLimit
	}
	return nil
}

// QuotaFor is a user's watch quota: admins have none.
func QuotaFor(role string, maxOpen, maxDaily int) Quota {
	if role == "admin" {
		return Quota{}
	}
	return Quota{MaxOpen: maxOpen, MaxDaily: maxDaily}
}

// Submission is the outcome of SubmitLink.
type Submission struct {
	Watch    db.Watch
	Existing bool // the user was already watching the article
}

// Submit starts watching a link for a user. It returns the user's existing
// open watch if there is one for the same article; that is free. Otherwise
// the new watch counts toward the quota.
func (s *Service) Submit(ctx context.Context, userID uuid.UUID, rawURL string, quota Quota) (db.Watch, error) {
	r, err := s.SubmitLink(ctx, userID, rawURL, quota)
	return r.Watch, err
}

// SubmitLink is Submit, also telling whether the watch already existed.
func (s *Service) SubmitLink(ctx context.Context, userID uuid.UUID, rawURL string, quota Quota) (Submission, error) {
	link, err := wechat.ParseLink(rawURL)
	if err != nil {
		return Submission{}, fmt.Errorf("%w: %w", ErrBadLink, err)
	}
	var w db.Watch
	existing := false
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		// One submission per user at a time: the duplicate check and the
		// quota hold until this watch is created.
		if _, err := q.LockUser(ctx, userID); err != nil {
			return err
		}
		now := s.now()

		art, err := q.GetArticleByAlias(ctx, link.Key)
		if errors.Is(err, pgx.ErrNoRows) {
			art, err = s.createArticle(ctx, q, link.Key)
		}
		if err != nil {
			return err
		}
		if w, err = q.GetOpenWatch(ctx, db.GetOpenWatchParams{UserID: userID, ArticleID: art.ID}); err == nil {
			existing = true
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		// A deleted or invalid article gives a watch that is over at once:
		// it counts toward the day's submissions but is not open.
		if art.Status == "pending" || art.Status == "alive" || art.Status == "dormant" {
			if err := checkOpen(ctx, q, userID, quota); err != nil {
				return err
			}
		}
		if quota.MaxDaily > 0 {
			n, err := q.CountWatchesSince(ctx, db.CountWatchesSinceParams{UserID: userID, Since: now.Add(-24 * time.Hour)})
			if err != nil {
				return err
			}
			if n >= int64(quota.MaxDaily) {
				return ErrDailyLimit
			}
		}

		p := db.CreateWatchParams{
			UserID: userID, ArticleID: &art.ID, SubmittedUrl: rawURL,
			ExpiresAt: now.AddDate(0, 0, s.cfg.WatchDays),
		}
		switch art.Status {
		case "pending":
			p.Status = "resolving"
			if _, err := s.jobs.InsertTx(ctx, tx, ResolveArgs{ArticleID: art.ID, URL: link.ServiceFetchURL()}, nil); err != nil {
				return err
			}
		case "alive":
			p.Status = "active"
		case "dormant":
			p.Status = "active"
			if err := q.ReactivateArticle(ctx, db.ReactivateArticleParams{ID: art.ID, NextCheckAt: now}); err != nil {
				return err
			}
		case "deleted":
			// Already gone: the watch ends at once, but gives access to the backup.
			p.Status, p.EndReason, p.EndedAt = "ended", ptr("deleted"), &now
		default: // invalid
			p.Status, p.EndReason, p.EndedAt = "failed", ptr("invalid"), &now
		}
		w, err = q.CreateWatch(ctx, p)
		return err
	})
	if err != nil {
		return Submission{}, err
	}
	return Submission{Watch: w, Existing: existing}, nil
}

// createArticle makes a pending article for a link nobody submitted before.
// If a concurrent submission registered the same link first, it uses theirs.
func (s *Service) createArticle(ctx context.Context, q *db.Queries, key string) (db.Article, error) {
	art, err := q.CreateArticle(ctx)
	if err != nil {
		return art, err
	}
	n, err := q.CreateAlias(ctx, db.CreateAliasParams{Url: key, ArticleID: art.ID})
	if err != nil || n == 1 {
		return art, err
	}
	if err := q.DeleteArticle(ctx, art.ID); err != nil {
		return art, err
	}
	return q.GetArticleByAlias(ctx, key)
}

// fetched is one page request and its analysis.
type fetched struct {
	mode   string
	page   *wechat.Page // nil when the request failed
	result wechat.Result
	at     time.Time
}

// fetchPage waits for the global limiter, fetches and classifies a page.
// It returns an error only when ctx ends; network failures become an
// "error" verdict like any other inconclusive answer.
func (s *Service) fetchPage(ctx context.Context, url string, mode wechat.FetchMode) (fetched, error) {
	f := fetched{mode: modeName(mode)}
	if err := s.limiter.Wait(ctx); err != nil {
		return f, err
	}
	sess := s.newSession()
	defer sess.Close()
	f.at = s.now()
	page, err := sess.FetchPage(ctx, url, mode)
	if ctx.Err() != nil {
		return f, ctx.Err()
	}
	if err != nil {
		f.result = wechat.Result{Verdict: wechat.VerdictError, Rule: "fetch_failed", Message: err.Error()}
	} else {
		f.page = page
		if mode == wechat.FetchHead {
			f.result = wechat.AnalyzeHead(page.Status, page.FinalURL, page.Body)
		} else {
			f.result = wechat.Analyze(page.Status, page.FinalURL, page.Body)
		}
	}
	if s.breaker.Record(f.result.Verdict) {
		until := s.breaker.OpenUntil()
		s.log.Warn("WeChat is blocking requests; pausing all fetches", "until", until)
		s.alert.Alert(ctx, "breaker", "微信频繁返回验证页，抓取已暂停",
			fmt.Sprintf("10 分钟内多次遇到验证页或限流，所有抓取暂停到 %s。最近一次：%s（%s）。\n频繁出现时考虑加大 FETCH_MIN_INTERVAL，或启用本地抓取节点（plan.md §11.3）。",
				until.Format(time.RFC3339), url, f.result.Rule))
	}
	return f, nil
}

func modeName(m wechat.FetchMode) string {
	switch m {
	case wechat.FetchFull:
		return "full"
	case wechat.FetchHead:
		return "head"
	default:
		return "check"
	}
}

func (s *Service) recordCheck(ctx context.Context, q *db.Queries, articleID uuid.UUID, f fetched, stateChange bool) error {
	r := f.result
	p := db.InsertCheckRunParams{
		ArticleID: articleID, CheckedAt: f.at, Mode: f.mode, Result: string(r.Verdict),
		Rule: nonEmpty(r.Rule), StateChange: stateChange,
	}
	if r.Reason != "" {
		p.Reason = ptr(string(r.Reason))
	}
	detail := strings.TrimSpace(strings.Join(nonBlank(r.Message, r.Detail), " | "))
	p.Detail = nonEmpty(truncate(detail, 500))
	if f.page != nil {
		p.HttpStatus = ptr(int32(f.page.Status))
		p.WireBytes = ptr(int32(min(f.page.WireBytes, 1<<31-1)))
		p.DurationMs = ptr(int32(f.page.Duration.Milliseconds()))
	}
	return q.InsertCheckRun(ctx, p)
}

// snoozeIfOpen returns a SnoozeError while the breaker is open.
func (s *Service) snoozeIfOpen() error {
	if until := s.breaker.OpenUntil(); !until.IsZero() {
		return &SnoozeError{After: time.Until(until) + jitter(time.Minute)}
	}
	return nil
}

func ptr[T any](v T) *T { return &v }

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nonBlank(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "…"
}

// TakeDeletionNews returns up to n of the user's watches whose article was
// deleted after since and that no Official Account reply has mentioned yet,
// oldest first, and marks them mentioned (plan.md §6.7). We cannot push to
// WeChat, so the next reply carries the news; the email is sent anyway.
func (s *Service) TakeDeletionNews(ctx context.Context, userID uuid.UUID, since time.Time, n int) ([]db.TakeWechatNewsRow, error) {
	rows, err := s.q.TakeWechatNews(ctx, db.TakeWechatNewsParams{Now: s.now(), UserID: userID, Since: since, MaxRows: int32(n)})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(rows, func(a, b db.TakeWechatNewsRow) int {
		if a.EndedAt == nil || b.EndedAt == nil {
			return 0
		}
		return a.EndedAt.Compare(*b.EndedAt)
	})
	return rows, nil
}
