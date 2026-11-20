package watch

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/MamaShip/Observer/internal/blob"
	"github.com/MamaShip/Observer/internal/store/db"
	"github.com/MamaShip/Observer/internal/store/storetest"
	"github.com/MamaShip/Observer/internal/wechat"
)

const (
	shortURL = "https://mp.weixin.qq.com/s/q-WkvTApjcwqtgk9LY7Q-A"
	// The same article as a long link, as a user might paste it.
	longURL  = "https://mp.weixin.qq.com/s?__biz=MzA3Mjk4NDU3MQ==&mid=2652612955&idx=1&sn=c82b5aaf0462dcc9212bc4d6089dad4c&scene=21#wechat_redirect"
	longKey  = "https://mp.weixin.qq.com/s?__biz=MzA3Mjk4NDU3MQ==&mid=2652612955&idx=1&sn=c82b5aaf0462dcc9212bc4d6089dad4c"
	longPage = longKey + "&chksm=" + wechat.PlaceholderChksm
	canonKey = "MzA3Mjk4NDU3MQ==:2652612955:1"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../wechat/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fakeWeb serves pages by URL; every image URL returns a distinct PNG.
type fakeWeb struct {
	mu     sync.Mutex
	pages  map[string][]byte
	assets int
}

func (w *fakeWeb) set(url string, body []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pages[url] = body
}

type fakeSession struct{ w *fakeWeb }

func (s fakeSession) FetchPage(_ context.Context, url string, _ wechat.FetchMode) (*wechat.Page, error) {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	body, ok := s.w.pages[url]
	if !ok {
		return nil, errors.New("connection refused: " + url)
	}
	return &wechat.Page{URL: url, FinalURL: url, Status: 200, Body: body, WireBytes: int64(len(body))}, nil
}

func (s fakeSession) FetchAsset(_ context.Context, url, _ string) (*wechat.Asset, error) {
	s.w.mu.Lock()
	s.w.assets++
	s.w.mu.Unlock()
	body := append([]byte("\x89PNG\r\n\x1a\n"), url...)
	return &wechat.Asset{URL: url, Status: 200, ContentType: "image/png", Size: int64(len(body)), Body: body, SHA256: sha256.Sum256(body)}, nil
}

func (fakeSession) Close() {}

// fakeJobs records inserted jobs instead of running them.
type fakeJobs struct {
	mu   sync.Mutex
	jobs []river.JobArgs
}

func (j *fakeJobs) Insert(_ context.Context, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.jobs = append(j.jobs, args)
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{Kind: args.Kind()}}, nil
}

func (j *fakeJobs) InsertTx(ctx context.Context, _ pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return j.Insert(ctx, args, opts)
}

// take removes and returns the queued jobs of type T.
func take[T river.JobArgs](j *fakeJobs) []T {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []T
	rest := j.jobs[:0]
	for _, a := range j.jobs {
		if v, ok := a.(T); ok {
			out = append(out, v)
		} else {
			rest = append(rest, a)
		}
	}
	j.jobs = rest
	return out
}

type env struct {
	t    *testing.T
	ctx  context.Context
	svc  *Service
	pool *pgxpool.Pool
	q    *db.Queries
	web  *fakeWeb
	jobs *fakeJobs
	blob *blob.Dir
}

func newEnv(t *testing.T) *env {
	pool, _ := storetest.NewPool(t)
	dir, err := blob.NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	web := &fakeWeb{pages: map[string][]byte{}}
	svc := New(Deps{
		Pool: pool, Blob: dir, Log: storetest.Logger(),
		NewSession: func() Session { return fakeSession{web} },
		Config:     Config{WatchDays: 60},
	})
	jobs := &fakeJobs{}
	svc.SetJobs(jobs)
	return &env{t: t, ctx: context.Background(), svc: svc, pool: pool, q: db.New(pool), web: web, jobs: jobs, blob: dir}
}

func (e *env) user(email string) uuid.UUID {
	e.t.Helper()
	u, err := e.q.UpsertUserByEmail(e.ctx, email)
	if err != nil {
		e.t.Fatal(err)
	}
	return u.ID
}

func (e *env) submit(user uuid.UUID, url string) db.Watch {
	e.t.Helper()
	w, err := e.svc.Submit(e.ctx, user, url)
	if err != nil {
		e.t.Fatalf("Submit(%s): %v", url, err)
	}
	return w
}

func (e *env) watch(id uuid.UUID) db.Watch {
	e.t.Helper()
	var w db.Watch
	err := e.pool.QueryRow(e.ctx, "SELECT id, user_id, article_id, submitted_url, status, end_reason, created_at, expires_at, ended_at, notified_at FROM watches WHERE id = $1", id).
		Scan(&w.ID, &w.UserID, &w.ArticleID, &w.SubmittedUrl, &w.Status, &w.EndReason, &w.CreatedAt, &w.ExpiresAt, &w.EndedAt, &w.NotifiedAt)
	if err != nil {
		e.t.Fatal(err)
	}
	return w
}

func (e *env) article(id uuid.UUID) db.Article {
	e.t.Helper()
	a, err := e.q.GetArticle(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return a
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// runResolve runs the one queued resolve job.
func (e *env) runResolve(final bool) (ResolveArgs, error) {
	e.t.Helper()
	jobs := take[ResolveArgs](e.jobs)
	if len(jobs) != 1 {
		e.t.Fatalf("%d resolve jobs queued, want 1", len(jobs))
	}
	return jobs[0], e.svc.Resolve(e.ctx, jobs[0], final)
}

// aliveArticle submits the fixture article and runs resolve and snapshot.
func (e *env) aliveArticle(user uuid.UUID) (db.Watch, db.Article) {
	e.t.Helper()
	e.web.set(shortURL, fixture(e.t, "alive_article.html"))
	w := e.submit(user, shortURL)
	if _, err := e.runResolve(false); err != nil {
		e.t.Fatal(err)
	}
	for _, s := range take[SnapshotArgs](e.jobs) {
		if err := e.svc.Snapshot(e.ctx, s); err != nil {
			e.t.Fatal(err)
		}
	}
	return e.watch(w.ID), e.article(*w.ArticleID)
}

func TestSubmitResolveSnapshot(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@example.com")
	e.web.set(shortURL, fixture(t, "alive_article.html"))

	w := e.submit(alice, shortURL+"?scene=1#rd")
	if w.Status != "resolving" || w.ArticleID == nil {
		t.Fatalf("watch = %+v", w)
	}
	if d := w.ExpiresAt.Sub(w.CreatedAt); d < 59*24*time.Hour || d > 61*24*time.Hour {
		t.Errorf("watch period = %s, want 60 days", d)
	}
	args, err := e.runResolve(false)
	if err != nil {
		t.Fatal(err)
	}
	if args.URL != shortURL {
		t.Errorf("resolve URL = %s", args.URL)
	}

	art := e.article(*w.ArticleID)
	if art.Status != "alive" || deref(art.CanonicalKey) != canonKey || deref(art.CanonicalUrl) != shortURL {
		t.Fatalf("article = %s key=%s url=%s", art.Status, deref(art.CanonicalKey), deref(art.CanonicalUrl))
	}
	if deref(art.AccountName) != "健康成都官微" || art.PublishedAt == nil || art.PublishedAt.Unix() != 1590541039 {
		t.Errorf("metadata: account=%q published=%v", deref(art.AccountName), art.PublishedAt)
	}
	if d := time.Until(*art.NextCheckAt); d < 40*time.Minute || d > 80*time.Minute {
		t.Errorf("next check in %s, want about an hour", d)
	}
	if got := e.watch(w.ID).Status; got != "active" {
		t.Errorf("watch status after resolve = %s", got)
	}
	if n := e.count("SELECT count(*) FROM check_runs WHERE article_id = $1 AND mode = 'full' AND state_change", art.ID); n != 1 {
		t.Errorf("%d first-fetch check runs", n)
	}

	snaps := take[SnapshotArgs](e.jobs)
	if len(snaps) != 1 || snaps[0].ArticleID != art.ID {
		t.Fatalf("snapshot jobs = %+v", snaps)
	}
	for range 2 { // idempotent
		if err := e.svc.Snapshot(e.ctx, snaps[0]); err != nil {
			t.Fatal(err)
		}
	}
	s, err := e.q.GetSnapshot(e.ctx, snaps[0].SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if s.AssetCount != 8 || s.MissingAssets != 0 || e.count("SELECT count(*) FROM assets") != 8 ||
		e.count("SELECT count(*) FROM snapshot_assets WHERE snapshot_id = $1", s.ID) != 8 {
		t.Errorf("snapshot assets = %d (missing %d)", s.AssetCount, s.MissingAssets)
	}
	if e.web.assets != 8 {
		t.Errorf("downloaded %d images, want 8 (second run must not refetch)", e.web.assets)
	}
	for _, key := range []string{s.HtmlKey, s.RawKey} {
		r, err := e.blob.Get(e.ctx, key)
		if err != nil {
			t.Fatalf("blob %s: %v", key, err)
		}
		r.Close()
	}

	// The same user submitting again gets the same watch.
	if again := e.submit(alice, shortURL); again.ID != w.ID {
		t.Errorf("resubmit created watch %s, want %s", again.ID, w.ID)
	}
}

func TestSecondLinkMergesIntoKnownArticle(t *testing.T) {
	e := newEnv(t)
	_, art := e.aliveArticle(e.user("alice@example.com"))

	// Bob pastes the long form; nothing knows it yet, so it starts pending.
	bob := e.user("bob@example.com")
	e.web.set(longPage, fixture(t, "alive_article.html"))
	wb := e.submit(bob, longURL)
	if wb.Status != "resolving" || *wb.ArticleID == art.ID {
		t.Fatalf("bob's watch = %+v", wb)
	}
	pendingID := *wb.ArticleID
	args, err := e.runResolve(false)
	if err != nil {
		t.Fatal(err)
	}
	if args.URL != longPage {
		t.Errorf("resolve requested %s, want the long link with a placeholder chksm", args.URL)
	}

	wb = e.watch(wb.ID)
	if wb.Status != "active" || *wb.ArticleID != art.ID {
		t.Fatalf("bob's watch after merge = %s on %s, want active on %s", wb.Status, wb.ArticleID, art.ID)
	}
	if n := e.count("SELECT count(*) FROM articles WHERE id = $1", pendingID); n != 0 {
		t.Error("pending duplicate article was not removed")
	}
	if jobs := take[SnapshotArgs](e.jobs); len(jobs) != 0 {
		t.Errorf("a second snapshot was queued for a known article: %+v", jobs)
	}
	if n := e.count("SELECT count(*) FROM check_runs WHERE article_id = $1", art.ID); n != 2 {
		t.Errorf("%d check runs on the merged article, want 2", n)
	}

	// The long link is now a known alias: Carol's watch is active at once.
	if wc := e.submit(e.user("carol@example.com"), longKey+"&chksm=abcdef12"); wc.Status != "active" || *wc.ArticleID != art.ID {
		t.Errorf("carol's watch = %s on %v", wc.Status, wc.ArticleID)
	}
}

func TestDeletionNeedsConfirmation(t *testing.T) {
	e := newEnv(t)
	w, art := e.aliveArticle(e.user("alice@example.com"))

	e.web.set(shortURL, fixture(t, "deleted_by_author.html"))
	if err := e.svc.Check(e.ctx, CheckArgs{ArticleID: art.ID}); err != nil {
		t.Fatal(err)
	}
	a := e.article(art.ID)
	if a.Status != "alive" || a.DeletionSuspectedAt == nil {
		t.Fatalf("after first deleted check: status=%s suspected=%v", a.Status, a.DeletionSuspectedAt)
	}
	if d := time.Until(*a.NextCheckAt); d < 9*time.Minute || d > 11*time.Minute {
		t.Errorf("confirmation scheduled in %s, want 10 minutes", d)
	}
	if e.watch(w.ID).Status != "active" {
		t.Error("watch ended on a single deleted check")
	}

	if err := e.svc.Check(e.ctx, CheckArgs{ArticleID: art.ID}); err != nil {
		t.Fatal(err)
	}
	a = e.article(art.ID)
	if a.Status != "deleted" || deref(a.DeletedReasonCode) != "author_deleted" || a.DeletedAt == nil || a.NextCheckAt != nil {
		t.Fatalf("after confirmation: status=%s reason=%s", a.Status, deref(a.DeletedReasonCode))
	}
	if got := e.watch(w.ID); got.Status != "ended" || deref(got.EndReason) != "deleted" {
		t.Errorf("watch = %s/%s, want ended/deleted", got.Status, deref(got.EndReason))
	}
	// Further checks do nothing; new watchers still get the backup.
	if err := e.svc.Check(e.ctx, CheckArgs{ArticleID: art.ID}); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM check_runs WHERE article_id = $1", art.ID); n != 3 {
		t.Errorf("%d check runs, want 3", n)
	}
	if wd := e.submit(e.user("dave@example.com"), shortURL); wd.Status != "ended" || deref(wd.EndReason) != "deleted" {
		t.Errorf("late watcher = %s/%s", wd.Status, deref(wd.EndReason))
	}
}

func TestDeletionNotConfirmed(t *testing.T) {
	e := newEnv(t)
	_, art := e.aliveArticle(e.user("alice@example.com"))
	e.web.set(shortURL, fixture(t, "deleted_violation.html"))
	if err := e.svc.Check(e.ctx, CheckArgs{ArticleID: art.ID}); err != nil {
		t.Fatal(err)
	}
	e.web.set(shortURL, fixture(t, "alive_article.html"))
	if err := e.svc.Check(e.ctx, CheckArgs{ArticleID: art.ID}); err != nil {
		t.Fatal(err)
	}
	if a := e.article(art.ID); a.Status != "alive" || a.DeletionSuspectedAt != nil {
		t.Errorf("status=%s suspected=%v, want alive and cleared", a.Status, a.DeletionSuspectedAt)
	}
}

func TestInconclusiveCheckBacksOff(t *testing.T) {
	e := newEnv(t)
	_, art := e.aliveArticle(e.user("alice@example.com"))
	e.web.set(shortURL, fixture(t, "blocked_verify.html"))
	for i, want := range []time.Duration{5 * time.Minute, 10 * time.Minute} {
		if err := e.svc.Check(e.ctx, CheckArgs{ArticleID: art.ID}); err != nil {
			t.Fatal(err)
		}
		a := e.article(art.ID)
		if a.Status != "alive" || a.ConsecutiveFailures != int32(i+1) {
			t.Fatalf("status=%s failures=%d", a.Status, a.ConsecutiveFailures)
		}
		if d := time.Until(*a.NextCheckAt); d < want*7/10 || d > want*13/10 {
			t.Errorf("retry %d in %s, want about %s", i+1, d, want)
		}
	}
	if n := e.count("SELECT count(*) FROM check_runs WHERE article_id = $1 AND result = 'blocked' AND rule = 'verify_page'", art.ID); n != 2 {
		t.Errorf("%d blocked check runs recorded", n)
	}
}

func TestResolveUnavailableArticle(t *testing.T) {
	e := newEnv(t)
	e.web.set(shortURL, fixture(t, "deleted_violation.html"))
	w := e.submit(e.user("alice@example.com"), shortURL)
	if _, err := e.runResolve(false); err != nil {
		t.Fatal(err)
	}
	if got := e.watch(w.ID); got.Status != "failed" || deref(got.EndReason) != "invalid" {
		t.Errorf("watch = %s/%s", got.Status, deref(got.EndReason))
	}
	a := e.article(*w.ArticleID)
	if a.Status != "invalid" || deref(a.DeletedReasonCode) != "violation" {
		t.Errorf("article = %s/%s", a.Status, deref(a.DeletedReasonCode))
	}
	if wb := e.submit(e.user("bob@example.com"), shortURL); wb.Status != "failed" {
		t.Errorf("second submitter's watch = %s", wb.Status)
	}
}

func TestResolveRetriesThenGivesUp(t *testing.T) {
	e := newEnv(t)
	e.web.set(shortURL, fixture(t, "blocked_verify.html"))
	w := e.submit(e.user("alice@example.com"), shortURL)
	args, err := e.runResolve(false)
	if err == nil {
		t.Fatal("blocked first fetch did not ask for a retry")
	}
	if e.watch(w.ID).Status != "resolving" {
		t.Fatal("watch left resolving before retries ran out")
	}
	if err := e.svc.Resolve(e.ctx, args, true); err != nil {
		t.Fatal(err)
	}
	if got := e.watch(w.ID); got.Status != "failed" || deref(got.EndReason) != "unreachable" {
		t.Errorf("watch = %s/%s, want failed/unreachable", got.Status, deref(got.EndReason))
	}
	// A later submission tries again.
	e.web.set(shortURL, fixture(t, "alive_article.html"))
	w2 := e.submit(e.user("alice@example.com"), shortURL)
	if _, err := e.runResolve(false); err != nil {
		t.Fatal(err)
	}
	if got := e.watch(w2.ID); got.Status != "active" {
		t.Errorf("resubmitted watch = %s", got.Status)
	}
}

func TestBreakerPausesFetching(t *testing.T) {
	e := newEnv(t)
	_, art := e.aliveArticle(e.user("alice@example.com"))
	e.web.set(shortURL, fixture(t, "blocked_verify.html"))
	for range 3 {
		if err := e.svc.Check(e.ctx, CheckArgs{ArticleID: art.ID}); err != nil {
			t.Fatal(err)
		}
	}
	var snooze *SnoozeError
	if err := e.svc.Check(e.ctx, CheckArgs{ArticleID: art.ID}); !errors.As(err, &snooze) || snooze.After < 29*time.Minute {
		t.Fatalf("check while blocked: %v, want a ~30 minute snooze", err)
	}
	if _, err := e.pool.Exec(e.ctx, "UPDATE articles SET next_check_at = now() - interval '1 minute'"); err != nil {
		t.Fatal(err)
	}
	if n, err := e.svc.ScheduleDue(e.ctx); err != nil || n != 0 {
		t.Errorf("ScheduleDue while open = %d, %v", n, err)
	}
}

func TestScheduleDue(t *testing.T) {
	e := newEnv(t)
	_, art := e.aliveArticle(e.user("alice@example.com"))
	if n, err := e.svc.ScheduleDue(e.ctx); err != nil || n != 0 {
		t.Fatalf("nothing due yet: %d, %v", n, err)
	}
	if _, err := e.q.RequestCheckNow(e.ctx, art.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := e.svc.ScheduleDue(e.ctx); err != nil || n != 1 {
		t.Fatalf("ScheduleDue = %d, %v", n, err)
	}
	if jobs := take[CheckArgs](e.jobs); len(jobs) != 1 || jobs[0].ArticleID != art.ID {
		t.Errorf("check jobs = %+v", jobs)
	}
}

func TestExpireAndReactivate(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@example.com")
	w, art := e.aliveArticle(alice)
	if _, err := e.pool.Exec(e.ctx, "UPDATE watches SET expires_at = now() - interval '1 second' WHERE id = $1", w.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Expire(e.ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.watch(w.ID); got.Status != "ended" || deref(got.EndReason) != "expired" || got.NotifiedAt == nil {
		t.Errorf("watch = %s/%s notified %v (expiries are announced as they end: never emailed)", got.Status, deref(got.EndReason), got.NotifiedAt)
	}
	if a := e.article(art.ID); a.Status != "dormant" || a.NextCheckAt != nil {
		t.Errorf("article = %s next=%v, want dormant", a.Status, a.NextCheckAt)
	}

	w2 := e.submit(alice, shortURL)
	if w2.Status != "active" || w2.ID == w.ID {
		t.Errorf("new watch = %+v", w2)
	}
	if a := e.article(art.ID); a.Status != "alive" || a.NextCheckAt == nil {
		t.Errorf("article after resubmit = %s", a.Status)
	}
}

func TestSubmitRejectsBadLinks(t *testing.T) {
	e := newEnv(t)
	for _, u := range []string{"https://example.com/s/abc", "not a url", "https://mp.weixin.qq.com/s?__biz=&timestamp=1&signature=x"} {
		if _, err := e.svc.Submit(e.ctx, e.user("alice@example.com"), u); !errors.Is(err, ErrBadLink) {
			t.Errorf("Submit(%q) = %v, want ErrBadLink", u, err)
		}
	}
}

func TestCanary(t *testing.T) {
	e := newEnv(t)
	deletedURL := "https://mp.weixin.qq.com/s/aIWmD5E2y-Yg7oMltd2i8w"
	e.svc.cfg.CanaryAliveURL, e.svc.cfg.CanaryDeletedURL = shortURL, deletedURL
	e.web.set(shortURL, fixture(t, "alive_article.html"))
	e.web.set(deletedURL, fixture(t, "deleted_by_author.html"))
	if bad, err := e.svc.Canary(e.ctx); err != nil || len(bad) != 0 {
		t.Fatalf("healthy canary: %v, %v", bad, err)
	}
	// WeChat changes its deletion page and detection no longer recognizes it.
	e.web.set(deletedURL, []byte(`<html><body><p>页面不存在</p></body></html>`))
	bad, err := e.svc.Canary(e.ctx)
	if err != nil || len(bad) != 1 || bad[0].URL != deletedURL || bad[0].Got != wechat.VerdictError {
		t.Fatalf("broken canary: %+v, %v", bad, err)
	}
}

// A user who submits two links of one article ends up with one watch.
func TestSameUserTwoLinksOneWatch(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@example.com")
	w1, art := e.aliveArticle(alice)
	e.web.set(longPage, fixture(t, "alive_article.html"))
	w2 := e.submit(alice, longURL)
	if _, err := e.runResolve(false); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM watches WHERE user_id = $1", alice); n != 1 {
		t.Fatalf("alice has %d watches, want 1", n)
	}
	if n := e.count("SELECT count(*) FROM watches WHERE id = $1", w2.ID); n != 0 {
		t.Error("duplicate watch was kept")
	}
	if got := e.watch(w1.ID); got.Status != "active" || *got.ArticleID != art.ID {
		t.Errorf("original watch = %s", got.Status)
	}
}
