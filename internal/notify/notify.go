// Package notify decides what email to send and when (plan.md §5.6): login
// codes, invitations, deletion digests and operator alerts, within the daily
// sending limit, recording every message in email_log.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/MamaShip/Observer/internal/auth"
	"github.com/MamaShip/Observer/internal/mail"
	"github.com/MamaShip/Observer/internal/store/db"
)

// Config holds the notifier settings.
type Config struct {
	ServiceName string // shown in emails
	BaseURL     string // links in emails point here
	DailyLimit  int    // provider's daily sending limit (Resend free tier: 100)
	// MonthlyLimit is how many notification emails one account gets per
	// month (Beijing time) unless its own monthly_email_limit says
	// otherwise; 0 means no limit.
	MonthlyLimit int
	AdminEmails  []string
}

// JobInserter is the part of the River client the notifier uses.
type JobInserter interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// Notifier sends the service's email.
type Notifier struct {
	pool   *pgxpool.Pool
	q      *db.Queries
	mailer mail.Mailer
	cfg    Config
	log    *slog.Logger
	jobs   JobInserter
	now    func() time.Time
}

// New builds a notifier. Call SetJobs before queueing digests or alerts.
func New(pool *pgxpool.Pool, mailer mail.Mailer, cfg Config, log *slog.Logger) *Notifier {
	return &Notifier{pool: pool, q: db.New(pool), mailer: mailer, cfg: cfg, log: log.With("component", "notify"), now: time.Now}
}

// SetJobs sets the job queue.
func (n *Notifier) SetJobs(j JobInserter) { n.jobs = j }

// ErrDailyLimit means today's sending budget is used up.
var ErrDailyLimit = errors.New("daily email limit reached")

// ErrJoinLimit means today's budget for login codes requested through
// public invitation links is used up; other email still goes out.
var ErrJoinLimit = errors.New("daily email budget for invitation links reached")

// RetryAt asks the job runner to try again at a given time without using up
// an attempt (the daily limit resets then).
type RetryAt struct{ Time time.Time }

func (r *RetryAt) Error() string { return "retry at " + r.Time.Format(time.RFC3339) }

// Job kinds.
type (
	// DigestArgs sends one user the digest of their ended watches.
	DigestArgs struct {
		UserID uuid.UUID `json:"user_id" river:"unique"`
	}
	// AlertArgs emails the operators. Alerts with the same Key are sent at
	// most once per AlertPeriod.
	AlertArgs struct {
		Key     string `json:"key" river:"unique"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	// SweepArgs is the periodic job that queues digests.
	SweepArgs struct{}
)

// Queue and timing.
const (
	SweepInterval = 5 * time.Minute
	AlertPeriod   = 6 * time.Hour
)

var openStates = []rivertype.JobState{
	rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable,
	rivertype.JobStateRunning, rivertype.JobStateScheduled,
}

func (DigestArgs) Kind() string { return "email_digest" }
func (DigestArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: mail.Queue, MaxAttempts: 8, UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: openStates}}
}
func (AlertArgs) Kind() string { return "email_alert" }
func (AlertArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: mail.Queue, MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: AlertPeriod}}
}
func (SweepArgs) Kind() string { return "notify_sweep" }

// limitFor is the daily cap for a kind of email. Logins may use the whole
// provider limit; everything else stops at 90% so people can still sign in
// after a burst of notifications.
func (n *Notifier) limitFor(kind string) int {
	if kind == "login" {
		return n.cfg.DailyLimit
	}
	return n.cfg.DailyLimit * 9 / 10
}

// JoinShare is the part of the daily limit that login codes requested
// through public invitation links may use, whatever the addresses: a link
// shared in a busy group must leave the rest to logins and notifications.
const JoinShare = 0.3

// JoinLimit is how many codes invitation links may send per UTC day.
func (n *Notifier) JoinLimit() int { return int(float64(n.cfg.DailyLimit) * JoinShare) }

// send delivers one message and records it in email_log.
func (n *Notifier) send(ctx context.Context, kind string, userID *uuid.UUID, m mail.Message) error {
	day := startOfDay(n.now())
	sent, err := n.q.CountEmailsSince(ctx, day)
	if err != nil {
		return err
	}
	if kind == "join" {
		joins, err := n.q.CountEmailsOfKindSince(ctx, db.CountEmailsOfKindSinceParams{Kind: kind, Since: day})
		if err != nil {
			return err
		}
		if int(joins) >= n.JoinLimit() || int(sent) >= n.limitFor(kind) {
			return ErrJoinLimit
		}
	}
	if int(sent) >= n.limitFor(kind) {
		return ErrDailyLimit
	}
	logID, err := n.q.InsertEmailLog(ctx, db.InsertEmailLogParams{UserID: userID, Kind: kind, ToEmail: m.To, Subject: m.Subject})
	if err != nil {
		return err
	}
	msgID, sendErr := n.mailer.Send(ctx, m)
	fin := db.FinishEmailLogParams{ID: logID, Status: "sent", ProviderID: nonEmpty(msgID)}
	if sendErr != nil {
		fin.Status, fin.LastError = "failed", nonEmpty(truncate(sendErr.Error(), 500))
	} else {
		now := n.now()
		fin.SentAt = &now
	}
	// Record the outcome even if the request was cancelled meanwhile.
	if err := n.q.FinishEmailLog(context.WithoutCancel(ctx), fin); err != nil {
		n.log.Error("could not record email outcome", "email_log", logID, "err", err)
	}
	if errors.Is(sendErr, mail.ErrQuota) {
		return fmt.Errorf("%w: %w", ErrDailyLimit, sendErr)
	}
	if sendErr == nil && kind != "alert" && int(sent)+1 == n.cfg.DailyLimit*8/10 {
		// This message reached 80% of the daily limit.
		n.Alert(ctx, "email-80", "今日邮件用量达到 80%",
			fmt.Sprintf("今天已发送 %d 封，额度 %d。经常出现时请考虑更换邮件服务商或升级套餐。", sent+1, n.cfg.DailyLimit))
	}
	return sendErr
}

// SendLogin emails a login code right away. It is not queued: the code would
// otherwise sit in the job table in plain text.
func (n *Notifier) SendLogin(ctx context.Context, to, code string, userID *uuid.UUID, valid time.Duration) error {
	m, err := mail.Render("login", to, mail.LoginData{
		Code: code, ValidMinutes: int(valid.Minutes()), ServiceName: n.cfg.ServiceName, ServiceURL: n.cfg.BaseURL,
	})
	if err != nil {
		return err
	}
	return n.send(ctx, "login", userID, m)
}

// SendJoinCode is SendLogin for a code requested through a public
// invitation link. It has its own daily budget (JoinLimit) and returns
// ErrJoinLimit beyond it, whether or not the address has an account.
func (n *Notifier) SendJoinCode(ctx context.Context, to, code string, userID *uuid.UUID, valid time.Duration) error {
	m, err := mail.Render("login", to, mail.LoginData{
		Code: code, ValidMinutes: int(valid.Minutes()), ServiceName: n.cfg.ServiceName, ServiceURL: n.cfg.BaseURL,
	})
	if err != nil {
		return err
	}
	return n.send(ctx, "join", userID, m)
}

// SendInvite shares the provider budget with notifications. Even when
// invitations are revoked and recreated, each address gets at most one an hour.
func (n *Notifier) SendInvite(ctx context.Context, to, token string, valid time.Duration) error {
	last, err := n.q.LastInviteMail(ctx, to)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if last != nil && n.now().Before(last.Add(time.Hour)) {
		return &RetryAt{Time: last.Add(time.Hour)}
	}
	m, err := mail.Render("invite", to, mail.InviteData{
		ServiceName: n.cfg.ServiceName, ServiceURL: n.cfg.BaseURL,
		Link: n.cfg.BaseURL + "/invite#t=" + token, ValidDays: int(valid.Hours() / 24),
	})
	if err != nil {
		return err
	}
	err = n.send(ctx, "invite", nil, m)
	if errors.Is(err, ErrDailyLimit) {
		return &RetryAt{Time: startOfDay(n.now()).Add(24*time.Hour + 5*time.Minute)}
	}
	return err
}

// Sweep queues a digest for every user with unannounced ended watches.
func (n *Notifier) Sweep(ctx context.Context) (int, error) {
	users, err := n.q.UsersWithPendingNotifications(ctx)
	if err != nil {
		return 0, err
	}
	queued := 0
	for _, u := range users {
		res, err := n.jobs.Insert(ctx, DigestArgs{UserID: u}, nil)
		if err != nil {
			return queued, err
		}
		if !res.UniqueSkippedAsDuplicate {
			queued++
		}
	}
	return queued, nil
}

// MonthStart is the start of t's calendar month in Beijing time, when the
// monthly notification count resets.
func MonthStart(t time.Time) time.Time { return auth.MonthStart(t) }

// Quota is an account's notification emails this month.
type Quota struct {
	Sent  int
	Limit int // 0: no limit
}

// MonthlyQuota reports how many notification emails user has had this
// month, and their limit.
func (n *Notifier) MonthlyQuota(ctx context.Context, user db.User) (Quota, error) {
	q := Quota{Limit: n.cfg.MonthlyLimit}
	if user.MonthlyEmailLimit != nil {
		q.Limit = int(*user.MonthlyEmailLimit)
	}
	sent, err := n.q.CountUserNotificationsSince(ctx, db.CountUserNotificationsSinceParams{UserID: &user.ID, Since: MonthStart(n.now())})
	q.Sent = int(sent)
	return q, err
}

// SendDigest emails a user about all their ended watches not yet announced,
// then marks them announced. Past the monthly limit it waits for next
// month, when one digest covers everything that happened meanwhile.
func (n *Notifier) SendDigest(ctx context.Context, userID uuid.UUID) error {
	user, err := n.q.GetUser(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // account deleted meanwhile
	}
	if err != nil {
		return err
	}
	if user.Status != "active" {
		return nil
	}
	rows, err := n.q.PendingNotifications(ctx, userID)
	if err != nil || len(rows) == 0 {
		return err
	}
	quota, err := n.MonthlyQuota(ctx, user)
	if err != nil {
		return err
	}
	if quota.Limit > 0 && quota.Sent >= quota.Limit {
		next := MonthStart(n.now()).AddDate(0, 1, 0).Add(5 * time.Minute)
		n.log.Info("monthly notification limit reached; digest postponed", "user", userID, "until", next)
		return &RetryAt{Time: next}
	}

	data := mail.DigestData{ServiceName: n.cfg.ServiceName, ServiceURL: n.cfg.BaseURL,
		MonthlyLimit: quota.Limit, LastThisMonth: quota.Limit > 0 && quota.Sent+1 == quota.Limit}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		item := mail.DigestItem{
			Title: deref(r.Title), AccountName: deref(r.AccountName), Reason: deref(r.DeletedReasonText),
			Link: n.cfg.BaseURL + "/watches/" + r.ID.String(),
		}
		if item.Title == "" {
			item.Title = r.SubmittedUrl
		}
		data.Items = append(data.Items, item)
		ids = append(ids, r.ID)
	}
	data.Deleted = len(data.Items)
	const kind = "deleted" // the only notification: expiries are not emailed
	m, err := mail.Render("digest", user.Email, data)
	if err != nil {
		return err
	}
	if err := n.send(ctx, kind, &userID, m); err != nil {
		if errors.Is(err, ErrDailyLimit) {
			n.log.Warn("daily email limit reached; digest postponed to tomorrow", "user", userID)
			return &RetryAt{Time: startOfDay(n.now()).Add(24*time.Hour + 5*time.Minute)}
		}
		return err
	}
	return n.q.MarkWatchesNotified(ctx, db.MarkWatchesNotifiedParams{Ids: ids, Now: n.now()})
}

// Alert queues an email to the operators. Alerts sharing a key are sent at
// most once per AlertPeriod; every call is logged.
func (n *Notifier) Alert(ctx context.Context, key, subject, body string) {
	n.log.Error("alert", "key", key, "subject", subject, "body", body)
	if len(n.cfg.AdminEmails) == 0 || n.jobs == nil {
		return
	}
	if _, err := n.jobs.Insert(ctx, AlertArgs{Key: key, Subject: subject, Body: body}, nil); err != nil {
		n.log.Error("could not queue alert email", "key", key, "err", err)
	}
}

// SendAlert emails an alert to every operator.
func (n *Notifier) SendAlert(ctx context.Context, a AlertArgs) error {
	var errs []error
	for _, to := range n.cfg.AdminEmails {
		m, err := mail.Render("alert", to, mail.AlertData{Subject: a.Subject, Body: a.Body})
		if err != nil {
			return err
		}
		if err := n.send(ctx, "alert", nil, m); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// startOfDay is midnight UTC, when providers' daily counters typically reset.
func startOfDay(t time.Time) time.Time {
	return t.UTC().Truncate(24 * time.Hour)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
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
	return strings.ToValidUTF8(s[:n], "") + "…"
}
