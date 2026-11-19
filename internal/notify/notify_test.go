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
	e.endedWatch(t, u.ID, "已到期文章", "expired")                                 // alice did not ask for expiry mail…
	if _, err := e.q.SkipExpiryNotifications(e.ctx, time.Now()); err != nil { // …so Expire marks it done
		t.Fatal(err)
	}

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

func TestExpiryOptIn(t *testing.T) {
	e := newEnv(t, 100)
	u, _ := e.q.CreateUser(e.ctx, "bob@example.com")
	if _, err := e.q.UpdateUserSettings(e.ctx, db.UpdateUserSettingsParams{ID: u.ID, NotifyOnExpire: true}); err != nil {
		t.Fatal(err)
	}
	e.endedWatch(t, u.ID, "到期文章", "expired")
	if n, _ := e.q.SkipExpiryNotifications(e.ctx, time.Now()); n != 0 {
		t.Fatalf("skipped %d expiry notices of a user who wants them", n)
	}
	if err := e.n.SendDigest(e.ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if s := e.mailer.Sent(); len(s) != 1 || s[0].Subject != "1 篇文章的观察期已结束" {
		t.Fatalf("sent = %+v", s)
	}
}

func TestDailyLimit(t *testing.T) {
	e := newEnv(t, 3) // notifications may use 2 of 3, logins all 3
	u, _ := e.q.CreateUser(e.ctx, "carol@example.com")
	for i := range 2 {
		if err := e.n.SendLogin(e.ctx, "carol@example.com", "https://x/l", &u.ID, 15*time.Minute); err != nil {
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
	if err := e.n.SendLogin(e.ctx, "carol@example.com", "https://x/l", &u.ID, 15*time.Minute); err != nil {
		t.Fatalf("third login (reserved share): %v", err)
	}
	if err := e.n.SendLogin(e.ctx, "carol@example.com", "https://x/l", &u.ID, 15*time.Minute); !errors.Is(err, ErrDailyLimit) {
		t.Fatalf("fourth login: %v, want ErrDailyLimit", err)
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
		if err := e.n.SendLogin(e.ctx, "x@example.com", "https://x/l", nil, time.Minute); err != nil {
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
