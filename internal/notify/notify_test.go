package notify

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/MamaShip/Observer/internal/mail"
	"github.com/MamaShip/Observer/internal/store/db"
	"github.com/MamaShip/Observer/internal/store/storetest"
)

type fakeJobs struct{ jobs []river.JobArgs }

func (f *fakeJobs) Insert(_ context.Context, a river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	f.jobs = append(f.jobs, a)
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{}}, nil
}

type env struct {
	ctx    context.Context
	n      *Notifier
	pool   *pgxpool.Pool
	q      *db.Queries
	mailer *mail.Memory
	jobs   *fakeJobs
}

func newEnv(t *testing.T, limit int) *env {
	pool, _ := storetest.NewPool(t)
	m := mail.NewMemory(nil)
	n := New(pool, m, Config{ServiceName: "折光观察者", BaseURL: "https://observer.example", DailyLimit: limit,
		AdminEmails: []string{"ops@example.com"}}, storetest.Logger())
	j := &fakeJobs{}
	n.SetJobs(j)
	return &env{ctx: context.Background(), n: n, pool: pool, q: db.New(pool), mailer: m, jobs: j}
}

// endedWatch creates an article and a watch that ended for reason.
func (e *env) endedWatch(t *testing.T, user uuid.UUID, title, reason string) uuid.UUID {
	t.Helper()
	var art, w uuid.UUID
	if err := e.pool.QueryRow(e.ctx, `INSERT INTO articles (status, title, account_name, deleted_reason_text)
		VALUES ('deleted', $1, '某公众号', '该内容已被发布者删除') RETURNING id`, title).Scan(&art); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(e.ctx, `INSERT INTO watches (user_id, article_id, submitted_url, status, end_reason, expires_at, ended_at)
		VALUES ($1, $2, 'https://mp.weixin.qq.com/s/x', 'ended', $3, now(), now()) RETURNING id`, user, art, reason).Scan(&w); err != nil {
		t.Fatal(err)
	}
	return w
}

func (e *env) emailLog(t *testing.T) map[string]int {
	t.Helper()
	rows, err := e.pool.Query(e.ctx, "SELECT kind || '/' || status, count(*) FROM email_log GROUP BY 1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			t.Fatal(err)
		}
		out[k] = n
	}
	return out
}

func TestDigest(t *testing.T) {
	e := newEnv(t, 100)
	u, err := e.q.CreateUser(e.ctx, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	w1 := e.endedWatch(t, u.ID, "文章一", "deleted")
	e.endedWatch(t, u.ID, "文章二", "deleted")
	e.endedWatch(t, u.ID, "已到期文章", "expired") // expiries are never emailed, even if left unannounced

	if n, err := e.n.Sweep(e.ctx); err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v", n, err)
	}
	args := e.jobs.jobs[0].(DigestArgs)
	if err := e.n.SendDigest(e.ctx, args.UserID); err != nil {
		t.Fatal(err)
	}
	sent := e.mailer.Sent()
	if len(sent) != 1 {
		t.Fatalf("%d emails sent, want one digest", len(sent))
	}
	m := sent[0]
	if m.To != "alice@example.com" || m.Subject != "你观察的 2 篇文章已被删除" ||
		!strings.Contains(m.Text, "文章一") || !strings.Contains(m.Text, "文章二") || strings.Contains(m.Text, "已到期文章") ||
		!strings.Contains(m.Text, "https://observer.example/watches/"+w1.String()) {
		t.Errorf("digest = %+v", m)
	}
	if got := e.emailLog(t); got["deleted/sent"] != 1 {
		t.Errorf("email_log = %v", got)
	}

	// Everything announced: nothing more to do.
	e.jobs.jobs = nil
	if n, _ := e.n.Sweep(e.ctx); n != 0 {
		t.Errorf("second sweep queued %d digests", n)
	}
	if err := e.n.SendDigest(e.ctx, u.ID); err != nil || len(e.mailer.Sent()) != 1 {
		t.Errorf("second digest sent mail (err %v)", err)
	}
}

func TestDailyLimit(t *testing.T) {
	e := newEnv(t, 3) // notifications may use 2 of 3, logins all 3
	u, _ := e.q.CreateUser(e.ctx, "carol@example.com")
	for i := range 2 {
		if err := e.n.SendLogin(e.ctx, "carol@example.com", "123456", &u.ID, 15*time.Minute); err != nil {
			t.Fatalf("login %d: %v", i, err)
		}
	}
	e.endedWatch(t, u.ID, "文章", "deleted")
	var retry *RetryAt
	if err := e.n.SendDigest(e.ctx, u.ID); !errors.As(err, &retry) {
		t.Fatalf("digest over the notification budget: %v, want RetryAt", err)
	}
	if !retry.Time.After(time.Now()) || time.Until(retry.Time) > 25*time.Hour {
		t.Errorf("retry at %s", retry.Time)
	}
	if err := e.n.SendLogin(e.ctx, "carol@example.com", "123456", &u.ID, 15*time.Minute); err != nil {
		t.Fatalf("third login (reserved share): %v", err)
	}
	if err := e.n.SendLogin(e.ctx, "carol@example.com", "123456", &u.ID, 15*time.Minute); !errors.Is(err, ErrDailyLimit) {
		t.Fatalf("fourth login: %v, want ErrDailyLimit", err)
	}
}

func TestInvitationMailLimits(t *testing.T) {
	e := newEnv(t, 2) // invitations may use 1 of 2, like notifications
	const recipient = "guest@example.com"
	const validity = 7 * 24 * time.Hour
	now := time.Date(2026, 11, 21, 3, 0, 0, 0, time.UTC)
	e.n.now = func() time.Time { return now }
	e.mailer.FailWith(errors.New("temporary SMTP failure"))
	if err := e.n.SendInvite(e.ctx, recipient, "first", validity); err == nil {
		t.Fatal("missing SMTP failure")
	}
	e.mailer.FailWith(nil)
	if err := e.n.SendInvite(e.ctx, recipient, "retry", validity); err != nil {
		t.Fatal(err)
	}
	var retry *RetryAt
	if err := e.n.SendInvite(e.ctx, recipient, "another", validity); !errors.As(err, &retry) || !retry.Time.Equal(now.Add(time.Hour)) {
		t.Fatalf("same-address retry: %v", err)
	}
	if err := e.n.SendInvite(e.ctx, "another@example.com", "other", validity); !errors.As(err, &retry) || retry.Time.Before(startOfDay(now).Add(24*time.Hour)) {
		t.Fatalf("daily budget retry: %v", err)
	}
	if len(e.mailer.Sent()) != 1 {
		t.Fatal("unexpected number of delivered invitations")
	}
}

func TestProviderQuotaPostponesDigest(t *testing.T) {
	e := newEnv(t, 100)
	u, _ := e.q.CreateUser(e.ctx, "dave@example.com")
	e.endedWatch(t, u.ID, "文章", "deleted")
	e.mailer.FailWith(mail.ErrQuota)
	var retry *RetryAt
	if err := e.n.SendDigest(e.ctx, u.ID); !errors.As(err, &retry) {
		t.Fatalf("provider quota: %v, want RetryAt", err)
	}
	if got := e.emailLog(t); got["deleted/failed"] != 1 {
		t.Errorf("email_log = %v", got)
	}
	e.mailer.FailWith(nil)
	if err := e.n.SendDigest(e.ctx, u.ID); err != nil || len(e.mailer.Sent()) != 1 {
		t.Fatalf("retry after quota: %v", err)
	}
}

func TestAlerts(t *testing.T) {
	e := newEnv(t, 100)
	e.n.Alert(e.ctx, "canary", "金丝雀检测失败", "详情")
	if len(e.jobs.jobs) != 1 {
		t.Fatalf("alert jobs = %v", e.jobs.jobs)
	}
	if err := e.n.SendAlert(e.ctx, e.jobs.jobs[0].(AlertArgs)); err != nil {
		t.Fatal(err)
	}
	if s := e.mailer.Sent(); len(s) != 1 || s[0].To != "ops@example.com" || s[0].Subject != "[Observer 告警] 金丝雀检测失败" {
		t.Fatalf("sent = %+v", s)
	}
}

func TestEightyPercentAlert(t *testing.T) {
	e := newEnv(t, 10)
	for range 8 {
		if err := e.n.SendLogin(e.ctx, "x@example.com", "123456", nil, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	alerts := 0
	for _, j := range e.jobs.jobs {
		if a, ok := j.(AlertArgs); ok && a.Key == "email-80" {
			alerts++
		}
	}
	if alerts != 1 {
		t.Fatalf("%d usage alerts after reaching 8 of 10, want exactly 1", alerts)
	}
}

func TestMonthlyLimit(t *testing.T) {
	e := newEnv(t, 100)
	e.n.cfg.MonthlyLimit = 3
	u, err := e.q.CreateUser(e.ctx, "carol@example.com")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-11-20 12:00 Beijing.
	now := time.Date(2026, 11, 20, 4, 0, 0, 0, time.UTC)
	e.n.now = func() time.Time { return now }
	// Login codes do not count toward the limit.
	if err := e.n.SendLogin(e.ctx, u.Email, "123456", &u.ID, time.Minute); err != nil {
		t.Fatal(err)
	}

	digest := func(title string) error {
		t.Helper()
		e.endedWatch(t, u.ID, title, "deleted")
		return e.n.SendDigest(e.ctx, u.ID)
	}
	for i, title := range []string{"一", "二", "三"} {
		if err := digest(title); err != nil {
			t.Fatalf("digest %d: %v", i+1, err)
		}
		last := e.mailer.Sent()[len(e.mailer.Sent())-1]
		if note := strings.Contains(last.Text, "已达到每月上限"); note != (i == 2) {
			t.Errorf("digest %d has the limit note: %v", i+1, note)
		}
	}
	q, err := e.n.MonthlyQuota(e.ctx, u)
	if err != nil || q != (Quota{Sent: 3, Limit: 3}) {
		t.Fatalf("quota = %+v, %v", q, err)
	}

	// The fourth waits for December 1st, 00:05 Beijing time.
	sent := len(e.mailer.Sent())
	err = digest("四")
	var retry *RetryAt
	if !errors.As(err, &retry) || !retry.Time.Equal(time.Date(2026, 11, 30, 16, 5, 0, 0, time.UTC)) {
		t.Fatalf("over the limit: %v", err)
	}
	if len(e.mailer.Sent()) != sent {
		t.Error("an email was sent over the limit")
	}
	if pending, _ := e.q.PendingNotifications(e.ctx, u.ID); len(pending) != 1 {
		t.Errorf("%d pending notifications, want the 1 held back", len(pending))
	}

	// Next month the held-back deletion goes out, together with new ones.
	now = time.Date(2026, 11, 30, 16, 5, 0, 0, time.UTC)
	e.endedWatch(t, u.ID, "五", "deleted")
	if err := e.n.SendDigest(e.ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	last := e.mailer.Sent()[len(e.mailer.Sent())-1]
	if !strings.Contains(last.Text, "四") || !strings.Contains(last.Text, "五") || !strings.Contains(last.Subject, "2 篇") {
		t.Errorf("next month's digest: %s\n%s", last.Subject, last.Text)
	}

	// A per-account limit overrides the default; 0 means none.
	for _, c := range []struct {
		limit *int32
		want  int
	}{{ptr(int32(0)), 0}, {ptr(int32(50)), 50}, {nil, 3}} {
		if _, err := e.q.SetMonthlyEmailLimit(e.ctx, db.SetMonthlyEmailLimitParams{Email: u.Email, MonthlyEmailLimit: c.limit}); err != nil {
			t.Fatal(err)
		}
		u, _ = e.q.GetUser(e.ctx, u.ID)
		if q, _ := e.n.MonthlyQuota(e.ctx, u); q.Limit != c.want || q.Sent != 1 {
			t.Errorf("limit %v: quota %+v", c.limit, q)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestMonthStart(t *testing.T) {
	for in, want := range map[string]string{
		"2026-11-20T04:00:00Z": "2026-10-31T16:00:00Z",
		"2026-11-30T15:59:59Z": "2026-10-31T16:00:00Z",
		"2026-11-30T16:00:00Z": "2026-11-30T16:00:00Z", // Dec 1st 00:00 Beijing
		"2027-01-01T03:00:00Z": "2026-12-31T16:00:00Z",
	} {
		ti, _ := time.Parse(time.RFC3339, in)
		if got := MonthStart(ti).UTC().Format(time.RFC3339); got != want {
			t.Errorf("MonthStart(%s) = %s, want %s", in, got, want)
		}
	}
}
