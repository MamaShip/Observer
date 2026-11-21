package app

import (
	"context"
	"crypto/sha1" //nolint:gosec // WeChat's signature scheme
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MamaShip/Observer/internal/api"
	"github.com/MamaShip/Observer/internal/auth"
	"github.com/MamaShip/Observer/internal/config"
	"github.com/MamaShip/Observer/internal/mail"
	"github.com/MamaShip/Observer/internal/mp"
	"github.com/MamaShip/Observer/internal/store/storetest"
	"github.com/MamaShip/Observer/internal/watch"
	"github.com/MamaShip/Observer/internal/watch/watchtest"
)

const wechatToken = "testtoken"

// wechatUser sends messages as WeChat would for one follower.
type wechatUser struct {
	t      *testing.T
	addr   string
	openid string
	msgID  int
}

func (u *wechatUser) callbackURL(extra string) string {
	ts, nonce := strconv.FormatInt(time.Now().Unix(), 10), "n"+strconv.Itoa(u.msgID)
	parts := []string{wechatToken, ts, nonce}
	sort.Strings(parts)
	h := sha1.Sum([]byte(strings.Join(parts, ""))) //nolint:gosec // see import
	return fmt.Sprintf("http://%s%s?signature=%s&timestamp=%s&nonce=%s&openid=%s%s", u.addr, mp.Path, hex.EncodeToString(h[:]), ts, nonce, u.openid, extra)
}

// send posts a message body (without the envelope) and returns the reply
// text, or "" for no reply.
func (u *wechatUser) send(inner string) string {
	u.t.Helper()
	u.msgID++
	body := fmt.Sprintf(`<xml><ToUserName><![CDATA[gh_test]]></ToUserName><FromUserName><![CDATA[%s]]></FromUserName><CreateTime>%d</CreateTime>%s</xml>`,
		u.openid, time.Now().Unix(), inner)
	req, _ := http.NewRequest(http.MethodPost, u.callbackURL(""), strings.NewReader(body))
	req.Host = site
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		u.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		u.t.Fatalf("callback: %d %s", resp.StatusCode, b)
	}
	if string(b) == "success" {
		return ""
	}
	var r struct{ Content string }
	if err := xml.Unmarshal(b, &r); err != nil {
		u.t.Fatalf("reply %s: %v", b, err)
	}
	return r.Content
}

func (u *wechatUser) text(s string) string {
	return u.send(fmt.Sprintf(`<MsgType><![CDATA[text]]></MsgType><Content><![CDATA[%s]]></Content><MsgId>%d</MsgId>`, s, u.msgID+1))
}

func (u *wechatUser) event(e string) string {
	return u.send(`<MsgType><![CDATA[event]]></MsgType><Event><![CDATA[` + e + `]]></Event>`)
}

var (
	bindLinkRe  = regexp.MustCompile(`href="http://` + regexp.QuoteMeta(site) + `/wx/bind#t=([A-Za-z0-9_-]{43})"`)
	loginLinkRe = regexp.MustCompile(`href="http://` + regexp.QuoteMeta(site) + `/wx/go\?to=(/[a-z0-9/-]*)#t=([A-Za-z0-9_-]{43})"`)
)

func bindToken(t *testing.T, reply string) string {
	t.Helper()
	m := bindLinkRe.FindStringSubmatch(reply)
	if m == nil {
		t.Fatalf("no bind link in %q", reply)
	}
	return m[1]
}

// TestWechatFlow follows a WeChat user from following the account to
// watching an article and getting its deletion email (plan.md §6.7).
func TestWechatFlow(t *testing.T) {
	web := watchtest.NewWeb()
	web.Set(watchtest.AliveURL, watchtest.Fixture(t, "alive_article.html"))
	mailer := mail.NewMemory(nil)
	cfg := config.Config{
		DatabaseURL: storetest.EmptyDB(t), DBMaxConns: 8, BlobDir: t.TempDir(),
		WatchDays: 60, FetchMinInterval: time.Second,
		BaseURL: "http://" + site, SnapBaseURL: "http://" + snap, SigningKey: strings.Repeat("s", 32),
		RegistrationMode: auth.ModeOpen, ServiceName: "折光观察者", DailyEmailLimit: 100, MonthlyEmailLimit: 20,
		MaxOpenWatches: 50, MaxDailySubmissions: 30,
		WechatToken: wechatToken, WechatAccountID: "gh_test", WechatName: "折光观察者",
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
	addr := ln.Addr().String()
	wx := &wechatUser{t: t, addr: addr, openid: "oAlice"}

	// The platform checks the URL.
	req, _ := http.NewRequest(http.MethodGet, wx.callbackURL("&echostr=8642"), nil)
	req.Host = site
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(b) != "8642" {
		t.Fatalf("URL check: %d %q", resp.StatusCode, b)
	}
	var siteInfo api.Site
	(&client{t: t, addr: addr}).api(http.MethodGet, "/site", nil, 200, &siteInfo)
	if siteInfo.Wechat == nil || siteInfo.Wechat.Name != "折光观察者" {
		t.Errorf("site = %+v", siteInfo.Wechat)
	}

	// Following, then sending a link before binding.
	bindToken(t, wx.event("subscribe"))
	reply := wx.text("看看这篇 " + watchtest.AliveURL)
	if !strings.Contains(reply, "绑定后会自动开始观察") {
		t.Fatalf("unbound link reply: %s", reply)
	}
	tok := bindToken(t, reply)

	// The bind link opens the website: log in, confirm, and the article is watched.
	alice := &client{t: t, addr: addr}
	alice.api(http.MethodPost, "/wechat/bind", map[string]string{"token": tok}, http.StatusUnauthorized, nil)
	alice.login(mailer, "alice@example.com")
	var bound struct {
		WatchID     string `json:"watch_id"`
		SubmitError string `json:"submit_error"`
	}
	alice.api(http.MethodPost, "/wechat/bind", map[string]string{"token": tok}, 200, &bound)
	if bound.WatchID == "" || bound.SubmitError != "" {
		t.Fatalf("bind = %+v", bound)
	}
	alice.api(http.MethodPost, "/wechat/bind", map[string]string{"token": tok}, http.StatusGone, nil)
	var me api.Me
	alice.api(http.MethodGet, "/me", nil, 200, &me)
	if me.Wechat == nil {
		t.Fatal("me has no binding")
	}
	var w api.WatchDetail
	for deadline := time.Now().Add(30 * time.Second); w.Watch.Status != "active" || len(w.Snapshots) == 0; time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no backup after 30s: %+v", w)
		}
		alice.api(http.MethodGet, "/watches/"+bound.WatchID, nil, 200, &w)
	}

	// The same link again: already watched, with a link that signs in.
	reply = wx.text(watchtest.AliveURL)
	m := loginLinkRe.FindStringSubmatch(reply)
	if !strings.Contains(reply, "已经在观察中") || m == nil || m[1] != "/watches/"+bound.WatchID {
		t.Fatalf("again: %s", reply)
	}
	phone := &client{t: t, addr: addr} // WeChat's browser, not logged in
	resp, b = phone.do(http.MethodPost, site, api.Prefix+"/wechat/login", map[string]string{"token": m[2]})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login link: %d %s", resp.StatusCode, b)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == auth.CookieName {
			phone.cookie = ck.Value
		}
	}
	phone.api(http.MethodGet, m[1], nil, 200, nil)
	(&client{t: t, addr: addr}).api(http.MethodPost, "/wechat/login", map[string]string{"token": m[2]}, http.StatusGone, nil)

	// Another address of the same article: the reply waits for the first
	// fetch, which finds the article already watched.
	other := "https://mp.weixin.qq.com/s/another-address-of-it"
	web.Set(other, watchtest.Fixture(t, "alive_article.html"))
	if reply := wx.text(other); !strings.Contains(reply, "已经在观察中") || !strings.Contains(reply, "未完成ICP备案") {
		t.Errorf("another address: %s", reply)
	}

	if reply := wx.text("我的"); !strings.Contains(reply, "a***@example.com · 观察中 1 篇") || !strings.Contains(reply, "我市新型冠状病毒") {
		t.Errorf("list: %s", reply)
	}
	if reply := wx.text("你好"); !strings.Contains(reply, "发送「我的」") {
		t.Errorf("help: %s", reply)
	}

	// Deleted: the email goes to the bound address.
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
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		got := false
		for _, m := range mailer.Sent() {
			got = got || m.To == "alice@example.com" && strings.Contains(m.Subject, "已被删除")
		}
		if got {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no deletion email; sent: %+v", mailer.Sent())
		}
	}

	// We cannot push, so the next reply tells; only once.
	reply = wx.text("我的")
	if !strings.HasPrefix(reply, "【删除提醒】你观察的《") || !strings.Contains(reply, "/watches/"+bound.WatchID+"#t=") {
		t.Errorf("no deletion news: %s", reply)
	}
	if reply := wx.text("我的"); strings.Contains(reply, "删除提醒") {
		t.Errorf("told twice: %s", reply)
	}

	// Unfollowing forgets the binding.
	if r := wx.event("unsubscribe"); r != "" {
		t.Errorf("reply to unsubscribe: %q", r)
	}
	me = api.Me{}
	alice.api(http.MethodGet, "/me", nil, 200, &me)
	if me.Wechat != nil {
		t.Error("still bound after unfollowing")
	}
	var traces int
	if err := a.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM wechat_bindings) + (SELECT count(*) FROM wechat_tokens WHERE openid = 'oAlice')`).Scan(&traces); err != nil {
		t.Fatal(err)
	}
	if traces != 0 {
		t.Errorf("%d rows still mention the WeChat user", traces)
	}

	// Binding again, then unbinding from the website.
	tok = bindToken(t, wx.text("绑定"))
	alice.api(http.MethodPost, "/wechat/bind", map[string]string{"token": tok}, 200, nil)
	alice.api(http.MethodDelete, "/me/wechat", nil, http.StatusNoContent, nil)
	if r := wx.text("我的"); !strings.Contains(r, "还没有绑定") {
		t.Errorf("after unbinding on the website: %s", r)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// Without WECHAT_TOKEN the entry is off.
func TestWechatOff(t *testing.T) {
	cfg := config.Config{
		DatabaseURL: storetest.EmptyDB(t), DBMaxConns: 4, BlobDir: t.TempDir(), WatchDays: 60, FetchMinInterval: time.Second,
		BaseURL: "http://" + site, SnapBaseURL: "http://" + snap, RegistrationMode: auth.ModeOpen, DailyEmailLimit: 100,
	}
	a, err := New(context.Background(), cfg, storetest.Logger(), "test", Overrides{Mailer: mail.NewMemory(nil)})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, mp.Path}, {http.MethodPost, mp.Path}, {http.MethodPost, api.Prefix + "/wechat/login"},
	} {
		rec := httptest.NewRecorder()
		req, _ := http.NewRequest(tc.method, "http://"+site+tc.path+"?"+url.Values{"signature": {"x"}}.Encode(), strings.NewReader(`{"token":"x"}`))
		req.Host = site
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://"+site)
		a.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", tc.method, tc.path, rec.Code)
		}
	}
	var s api.Site
	rec := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "http://"+site+api.Prefix+"/site", nil)
	req.Host = site
	a.Handler().ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil || s.Wechat != nil {
		t.Errorf("site: %+v %v", s.Wechat, err)
	}
}
