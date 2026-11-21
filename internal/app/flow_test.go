package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MamaShip/Observer/internal/api"
	"github.com/MamaShip/Observer/internal/auth"
	"github.com/MamaShip/Observer/internal/config"
	"github.com/MamaShip/Observer/internal/mail"
	"github.com/MamaShip/Observer/internal/store/storetest"
	"github.com/MamaShip/Observer/internal/watch"
	"github.com/MamaShip/Observer/internal/watch/watchtest"
)

const (
	site = "observer.test"
	snap = "snap.observer.test"
)

// client talks to the running app as a browser on the main site would.
type client struct {
	t      *testing.T
	addr   string
	cookie string
}

func (c *client) do(method, host, target string, body any) (*http.Response, []byte) {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://"+c.addr+target, r)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Host = host
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", "http://"+site)
	}
	if c.cookie != "" {
		req.Header.Set("Cookie", auth.CookieName+"="+c.cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func (c *client) api(method, path string, body any, want int, out any) {
	c.t.Helper()
	resp, b := c.do(method, site, api.Prefix+path, body)
	if resp.StatusCode != want {
		c.t.Fatalf("%s %s = %d, want %d: %s", method, path, resp.StatusCode, want, b)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			c.t.Fatalf("%s %s: %v: %s", method, path, err, b)
		}
	}
}

var codeRe = regexp.MustCompile(`验证码：(\d{6})`)

// login requests a code for email and enters it, as the login page does.
func (c *client) login(mailer *mail.Memory, email string) {
	c.t.Helper()
	before := len(mailer.Sent())
	var quota struct {
		Remaining   int `json:"remaining"`
		ResendAfter int `json:"resend_after"`
	}
	c.api(http.MethodPost, "/auth/code", map[string]string{"email": email}, http.StatusAccepted, &quota)
	if quota.Remaining != auth.CodesPerDay-1 || quota.ResendAfter != int(auth.ResendAfter.Seconds()) {
		c.t.Errorf("quota = %+v", quota)
	}
	var code string
	for _, msg := range mailer.Sent()[before:] {
		m := codeRe.FindStringSubmatch(msg.Text)
		if msg.To == email && m != nil && strings.HasPrefix(msg.Subject, m[1]) {
			if code != "" {
				c.t.Fatal("received multiple login codes")
			}
			code = m[1]
		}
	}
	if code == "" {
		c.t.Fatalf("login code not sent to %s", email)
	}
	// Asking again right away is refused with a wait time.
	resp, b := c.do(http.MethodPost, site, api.Prefix+"/auth/code", map[string]string{"email": email})
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		c.t.Errorf("immediate resend: %d %s (Retry-After %q)", resp.StatusCode, b, resp.Header.Get("Retry-After"))
	}
	resp, b = c.do(http.MethodPost, site, api.Prefix+"/auth/verify", map[string]string{"email": email, "code": "abc"})
	if resp.StatusCode != http.StatusBadRequest {
		c.t.Errorf("bad code: %d %s", resp.StatusCode, b)
	}
	resp, b = c.do(http.MethodPost, site, api.Prefix+"/auth/verify", map[string]string{"email": email, "code": code})
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("verify: %d %s", resp.StatusCode, b)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == auth.CookieName {
			if !ck.Secure || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" {
				c.t.Errorf("session cookie attributes: %+v", ck)
			}
			c.cookie = ck.Value
		}
	}
	if c.cookie == "" {
		c.t.Fatal("no session cookie")
	}
}

// TestUserFlow is the M3 acceptance test: log in, watch an article, read
// its backup, see it deleted, and get the email.
func TestUserFlow(t *testing.T) {
	web := watchtest.NewWeb()
	web.Set(watchtest.AliveURL, watchtest.Fixture(t, "alive_article.html"))
	mailer := mail.NewMemory(nil)
	cfg := config.Config{
		DatabaseURL: storetest.EmptyDB(t), DBMaxConns: 8, BlobDir: t.TempDir(), CacheDir: t.TempDir(), CacheMaxBytes: 1 << 20,
		WatchDays: 60, FetchMinInterval: time.Second,
		BaseURL: "http://" + site, SnapBaseURL: "http://" + snap, SigningKey: strings.Repeat("s", 32),
		RegistrationMode: auth.ModeOpen, ServiceName: "折光观察者", DailyEmailLimit: 100, MonthlyEmailLimit: 20,
		MaxOpenWatches: 50, MaxDailySubmissions: 30,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := New(ctx, cfg, storetest.Logger(), "test", Overrides{
		NewSession: func() watch.Session { return web.Session() }, Mailer: mailer,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx, ln) }()
	alice := &client{t: t, addr: ln.Addr().String()}

	// Not logged in yet.
	alice.api(http.MethodGet, "/me", nil, http.StatusUnauthorized, nil)
	var session struct {
		User *api.Me `json:"user"`
	}
	alice.api(http.MethodGet, "/auth/session", nil, http.StatusOK, &session)
	if session.User != nil {
		t.Fatalf("visitor has a session user: %+v", session.User)
	}
	alice.login(mailer, "alice@example.com")
	alice.api(http.MethodGet, "/auth/session", nil, http.StatusOK, &session)
	if session.User == nil || session.User.Email != "alice@example.com" {
		t.Fatalf("session after login: %+v", session.User)
	}
	// An hour or more later the server extends the session and renews the
	// cookie with it, so the browser keeps it as long as the server does.
	resp, _ := alice.do(http.MethodGet, site, api.Prefix+"/me", nil)
	if len(resp.Cookies()) != 0 {
		t.Errorf("cookie renewed right after login: %v", resp.Cookies())
	}
	if _, err := a.pool.Exec(ctx, `UPDATE sessions SET last_seen_at = last_seen_at - interval '2 hours'`); err != nil {
		t.Fatal(err)
	}
	resp, _ = alice.do(http.MethodGet, site, api.Prefix+"/me", nil)
	if ck := resp.Cookies(); len(ck) != 1 || ck[0].Name != auth.CookieName || ck[0].Value != alice.cookie ||
		ck[0].MaxAge != int(auth.SessionTTL.Seconds()) || !ck[0].Secure || !ck[0].HttpOnly || ck[0].SameSite != http.SameSiteLaxMode {
		t.Errorf("renewed cookie: %v", ck)
	}
	var me api.Me
	alice.api(http.MethodGet, "/me", nil, 200, &me)
	if me.Email != "alice@example.com" || me.EmailsThisMonth != 0 || me.MonthlyEmails != 20 {
		t.Fatalf("me = %+v", me)
	}

	// Writes from another site are refused even with the cookie.
	resp, _ = alice.doWithOrigin(http.MethodPost, api.Prefix+"/watches", "https://evil.example", map[string]string{"url": watchtest.AliveURL})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site write: %d", resp.StatusCode)
	}

	// Submit and wait for the backup.
	var w api.WatchDetail
	alice.api(http.MethodPost, "/watches", map[string]string{"url": "https://example.com/not-wechat"}, http.StatusUnprocessableEntity, nil)
	alice.api(http.MethodPost, "/watches", map[string]string{"url": watchtest.AliveURL}, http.StatusAccepted, &w)
	if w.Watch.Status != "resolving" {
		t.Fatalf("new watch = %+v", w.Watch)
	}
	id := w.Watch.ID
	deadline := time.Now().Add(30 * time.Second)
	for w.Watch.Status != "active" || len(w.Snapshots) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no backup after 30s: %+v", w)
		}
		time.Sleep(200 * time.Millisecond)
		alice.api(http.MethodGet, "/watches/"+id.String(), nil, 200, &w)
	}
	if w.Watch.Article == nil || w.Watch.Article.AccountName != "健康成都官微" || len(w.Checks) != 1 {
		t.Fatalf("watch detail = %+v", w)
	}
	var list struct {
		Items      []api.Watch `json:"items"`
		NextCursor string      `json:"next_cursor"`
	}
	alice.api(http.MethodGet, "/watches?status=active", nil, 200, &list)
	if len(list.Items) != 1 || list.Items[0].ID != id || list.NextCursor != "" {
		t.Fatalf("list = %+v", list)
	}

	// Read the backup on the snapshot origin.
	snapID := w.Snapshots[0].ID
	var link struct {
		URL string `json:"url"`
	}
	alice.api(http.MethodPost, "/snapshots/"+snapID.String()+"/view-url", nil, 200, &link)
	u, _ := url.Parse(link.URL)
	if u.Host != snap {
		t.Fatalf("view URL %s is not on the snapshot origin", link.URL)
	}
	resp, page := alice.do(http.MethodGet, snap, u.RequestURI(), nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(page), "截至5月26日24时") ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors http://"+site) {
		t.Fatalf("snapshot page: %d %s", resp.StatusCode, resp.Header)
	}
	asset := regexp.MustCompile(`/a/[0-9a-f]{64}`).FindString(string(page))
	if resp, _ := alice.do(http.MethodGet, snap, asset, nil); resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Errorf("image %s: %d", asset, resp.StatusCode)
	}
	if resp, b := alice.do(http.MethodGet, site, u.RequestURI(), nil); strings.Contains(string(b), "截至5月26日24时") {
		t.Errorf("snapshot served on the main origin: %d", resp.StatusCode)
	}
	alice.api(http.MethodPost, "/snapshots/"+snapID.String()+"/download-url", nil, 200, &link)
	u, _ = url.Parse(link.URL)
	resp, single := alice.do(http.MethodGet, snap, u.RequestURI(), nil)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") ||
		bytes.Contains(single, []byte("/a/")) {
		t.Fatalf("download: %d %s", resp.StatusCode, resp.Header)
	}

	// Another user cannot read Alice's backup.
	bob := &client{t: t, addr: alice.addr}
	bob.login(mailer, "bob@example.com")
	bob.api(http.MethodPost, "/snapshots/"+snapID.String()+"/view-url", nil, http.StatusNotFound, nil)

	// Bob's second code of the day (a minute later), then "try tomorrow".
	if _, err := a.pool.Exec(ctx, `UPDATE login_requests SET last_at = last_at - interval '2 minutes' WHERE email = 'bob@example.com'`); err != nil {
		t.Fatal(err)
	}
	var quota struct {
		Remaining   int `json:"remaining"`
		ResendAfter int `json:"resend_after"`
	}
	bob.api(http.MethodPost, "/auth/code", map[string]string{"email": "bob@example.com"}, http.StatusAccepted, &quota)
	if quota.Remaining != 0 || quota.ResendAfter != 0 {
		t.Errorf("last code of the day: %+v", quota)
	}
	if _, err := a.pool.Exec(ctx, `UPDATE login_requests SET last_at = last_at - interval '2 minutes' WHERE email = 'bob@example.com'`); err != nil {
		t.Fatal(err)
	}
	if resp, b := bob.do(http.MethodPost, site, api.Prefix+"/auth/code", map[string]string{"email": "bob@example.com"}); resp.StatusCode != http.StatusTooManyRequests ||
		!strings.Contains(string(b), "请明天再试") {
		t.Errorf("third code of the day: %d %s", resp.StatusCode, b)
	}
	bob.api(http.MethodGet, "/watches/"+id.String(), nil, http.StatusNotFound, nil)

	// The article is deleted: two checks ten minutes apart confirm it, the
	// sweep emails Alice.
	web.Set(watchtest.AliveURL, watchtest.Fixture(t, "deleted_by_author.html"))
	for i := range 2 {
		if i == 1 {
			if _, err := a.pool.Exec(ctx, `UPDATE articles SET deletion_suspected_at = deletion_suspected_at - interval '10 minutes' WHERE id = $1`,
				w.Watch.Article.ID); err != nil {
				t.Fatal(err)
			}
		}
		if err := a.Watch.Check(ctx, watch.CheckArgs{ArticleID: w.Watch.Article.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Notify.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	var digest *mail.Message
	for deadline := time.Now().Add(15 * time.Second); digest == nil; {
		for _, m := range mailer.Sent() {
			if m.To == "alice@example.com" && strings.Contains(m.Subject, "已被删除") {
				digest = &m
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no deletion email; sent: %+v", mailer.Sent())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(digest.Text, "该内容已被发布者删除") || !strings.Contains(digest.Text, "http://"+site+"/watches/"+id.String()) {
		t.Errorf("deletion email = %s", digest.Text)
	}
	alice.api(http.MethodGet, "/watches/"+id.String(), nil, 200, &w)
	if w.Watch.Status != "ended" || w.Watch.EndReason != "deleted" || w.Watch.Article.DeletedReason != "author_deleted" {
		t.Fatalf("after deletion: %+v / %+v", w.Watch, w.Watch.Article)
	}
	alice.api(http.MethodPost, "/snapshots/"+snapID.String()+"/view-url", nil, 200, nil) // backup stays readable
	// The digest counts toward her monthly notifications (logged just after sending).
	for deadline := time.Now().Add(5 * time.Second); me.EmailsThisMonth == 0 && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		alice.api(http.MethodGet, "/me", nil, 200, &me)
	}
	if me.EmailsThisMonth != 1 || me.MonthlyEmails != 20 {
		t.Errorf("after the digest: %d of %d emails this month", me.EmailsThisMonth, me.MonthlyEmails)
	}

	// Stopping, logging out, deleting the account.
	alice.api(http.MethodDelete, "/watches/"+uuid.NewString(), nil, http.StatusNotFound, nil)
	alice.api(http.MethodDelete, "/watches/"+id.String(), nil, http.StatusNoContent, nil)
	// Logging out when the session is due for renewal still clears the cookie.
	if _, err := a.pool.Exec(ctx, `UPDATE sessions SET last_seen_at = last_seen_at - interval '2 hours'`); err != nil {
		t.Fatal(err)
	}
	resp, b := bob.do(http.MethodPost, site, api.Prefix+"/auth/logout", nil)
	if ck := resp.Cookies(); resp.StatusCode != http.StatusNoContent || len(ck) != 1 || ck[0].Name != auth.CookieName || ck[0].MaxAge >= 0 {
		t.Errorf("logout: %d %s, cookies %v", resp.StatusCode, b, ck)
	}
	bob.api(http.MethodGet, "/me", nil, http.StatusUnauthorized, nil)
	alice.api(http.MethodDelete, "/me", nil, http.StatusNoContent, nil)
	alice.api(http.MethodGet, "/me", nil, http.StatusUnauthorized, nil)
	// Her address is gone from every table (plan.md §12).
	var traces int
	if err := a.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM users WHERE email = 'alice@example.com')
		+ (SELECT count(*) FROM login_codes WHERE email = 'alice@example.com')
		+ (SELECT count(*) FROM email_log WHERE to_email = 'alice@example.com')`).Scan(&traces); err != nil {
		t.Fatal(err)
	}
	if traces != 0 {
		t.Errorf("%d rows still hold the deleted user's address", traces)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func (c *client) doWithOrigin(method, target, origin string, body any) (*http.Response, []byte) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, "http://"+c.addr+target, bytes.NewReader(b))
	req.Host = site
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	req.Header.Set("Cookie", auth.CookieName+"="+c.cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}
