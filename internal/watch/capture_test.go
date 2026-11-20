package watch

import (
	"bytes"
	"errors"
	"testing"

	"github.com/MamaShip/Observer/internal/wechat"
)

// A post whose page has no article body (picture and video posts) is online
// for the checker, but no reading page can be built: the capture says so
// instead of staying pending.
func TestCaptureWithoutContent(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@example.com")
	page := bytes.Replace(fixture(t, "alive_article.html"), []byte(`id="js_content"`), []byte(`id="js_other"`), 1)
	if r := wechat.Analyze(200, "", page); r.Verdict != wechat.VerdictAlive || r.Rule != "article_template_no_content_node" {
		t.Fatalf("test page: %s/%s", r.Verdict, r.Rule)
	}
	e.web.set(shortURL, page)
	w := e.submit(alice, shortURL)
	if _, err := e.runResolve(false); err != nil {
		t.Fatal(err)
	}
	snaps := take[SnapshotArgs](e.jobs)
	if len(snaps) != 1 {
		t.Fatalf("%d snapshot jobs", len(snaps))
	}
	c, err := e.q.GetCapture(e.ctx, snaps[0].SnapshotID)
	if err != nil || c.Status != "pending" || c.ArticleID != *w.ArticleID {
		t.Fatalf("capture before the build = %+v, %v", c, err)
	}
	if err := e.svc.Snapshot(e.ctx, snaps[0], false); !errors.Is(err, ErrPermanent) {
		t.Fatalf("Snapshot = %v, want a permanent error", err)
	}
	c, _ = e.q.GetCapture(e.ctx, c.ID)
	if c.Status != "failed" || deref(c.ErrorCode) != "no_content" || deref(c.Error) == "" {
		t.Errorf("capture = %s %s %q", c.Status, deref(c.ErrorCode), deref(c.Error))
	}
	if got := e.watch(w.ID).Status; got != "active" {
		t.Errorf("watch = %s; the article is still checked", got)
	}
}

// A capture that failed until its retries ran out is rebuilt from the stored
// raw page, without WeChat, even once the article is gone.
func TestRebuildCapture(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@example.com")
	e.web.set(shortURL, fixture(t, "alive_article.html"))
	e.submit(alice, shortURL)
	if _, err := e.runResolve(false); err != nil {
		t.Fatal(err)
	}
	args := take[SnapshotArgs](e.jobs)[0]
	c, _ := e.q.GetCapture(e.ctx, args.SnapshotID)

	// Storage is unreadable on the last attempt.
	r, err := e.blob.Get(e.ctx, c.RawKey)
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	_, _ = raw.ReadFrom(r)
	r.Close()
	if err := e.blob.Delete(e.ctx, c.RawKey); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Snapshot(e.ctx, args, false); err == nil || errors.Is(err, ErrPermanent) {
		t.Fatalf("an earlier attempt: %v, want a retryable error", err)
	}
	if c, _ := e.q.GetCapture(e.ctx, c.ID); c.Status != "pending" {
		t.Errorf("after a retryable failure: %s", c.Status)
	}
	if err := e.svc.Snapshot(e.ctx, args, true); err == nil {
		t.Fatal("the last attempt succeeded without the raw page")
	}
	if c, _ := e.q.GetCapture(e.ctx, c.ID); c.Status != "failed" || deref(c.ErrorCode) != "error" {
		t.Errorf("after the last attempt: %s %s", c.Status, deref(c.ErrorCode))
	}

	// The article is deleted meanwhile; the raw page comes back.
	if _, err := e.pool.Exec(e.ctx, "UPDATE articles SET status = 'deleted' WHERE id = $1", c.ArticleID); err != nil {
		t.Fatal(err)
	}
	if err := e.blob.Put(e.ctx, c.RawKey, bytes.NewReader(raw.Bytes()), int64(raw.Len()), "application/gzip"); err != nil {
		t.Fatal(err)
	}
	fetches := e.web.pageFetches()
	if err := e.svc.RebuildCapture(e.ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	again := take[SnapshotArgs](e.jobs)
	if len(again) != 1 || again[0].SnapshotID != c.ID {
		t.Fatalf("rebuild jobs = %+v", again)
	}
	if c, _ := e.q.GetCapture(e.ctx, c.ID); c.Status != "pending" || c.ErrorCode != nil {
		t.Errorf("queued again: %s %v", c.Status, c.ErrorCode)
	}
	if err := e.svc.Snapshot(e.ctx, again[0], false); err != nil {
		t.Fatal(err)
	}
	if c, _ := e.q.GetCapture(e.ctx, c.ID); c.Status != "done" {
		t.Errorf("rebuilt: %s", c.Status)
	}
	if _, err := e.q.GetSnapshot(e.ctx, c.ID); err != nil {
		t.Errorf("snapshot: %v", err)
	}
	if e.web.pageFetches() != fetches {
		t.Error("the rebuild fetched the page from WeChat")
	}
	if err := e.svc.RebuildCapture(e.ctx, c.ID); !errors.Is(err, ErrCaptureDone) {
		t.Errorf("rebuilding a done capture: %v", err)
	}
}
