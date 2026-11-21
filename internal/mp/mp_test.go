package mp

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MamaShip/Observer/internal/auth"
	"github.com/MamaShip/Observer/internal/store/db"
	"github.com/MamaShip/Observer/internal/watch"
)

// fakes

type fakeAccounts struct {
	bound     map[string]db.User
	bindLinks []string // pending URL of each bind link made
	logins    int
	forgot    []string
	bindErr   error
	pending   string
}

func (f *fakeAccounts) WechatBindingFor(_ context.Context, openid string) (auth.WechatBinding, bool, error) {
	u, ok := f.bound[openid]
	return auth.WechatBinding{OpenID: openid, User: u}, ok, nil
}

func (f *fakeAccounts) NewWechatBindLink(_ context.Context, _, pending string) (string, error) {
	f.bindLinks = append(f.bindLinks, pending)
	return "BINDTOKEN", nil
}

func (f *fakeAccounts) NewWechatLoginLink(context.Context, string, uuid.UUID) (string, error) {
	f.logins++
	return "LOGINTOKEN", nil
}

func (f *fakeAccounts) BindWechat(context.Context, uuid.UUID, string) (string, error) {
	return f.pending, f.bindErr
}

func (f *fakeAccounts) ForgetWechat(_ context.Context, openid string) (bool, error) {
	f.forgot = append(f.forgot, openid)
	_, ok := f.bound[openid]
	delete(f.bound, openid)
	return ok, nil
}

type fakeWatches struct {
	sub   watch.Submission
	err   error
	calls []string
}

func (f *fakeWatches) SubmitLink(_ context.Context, _ uuid.UUID, rawURL string, _ watch.Quota) (watch.Submission, error) {
	f.calls = append(f.calls, rawURL)
	return f.sub, f.err
}

type fakeStore struct {
	article db.Article
	rows    []db.ListWatchesPageRow
	open    int64
}

func (f *fakeStore) GetArticle(context.Context, uuid.UUID) (db.Article, error) { return f.article, nil }
func (f *fakeStore) ListWatchesPage(context.Context, db.ListWatchesPageParams) ([]db.ListWatchesPageRow, error) {
	return f.rows, nil
}
func (f *fakeStore) CountOpenWatches(context.Context, uuid.UUID) (int64, error) { return f.open, nil }

var now = time.Date(2026, 11, 21, 2, 0, 0, 0, time.UTC)

func newBot() (*Bot, *fakeAccounts, *fakeWatches, *fakeStore) {
	acc := &fakeAccounts{bound: map[string]db.User{}}
	ws, st := &fakeWatches{}, &fakeStore{}
	b := NewBot(acc, ws, st, Config{BaseURL: "https://ob.test", ServiceName: "折光观察者", WatchDays: 60, MaxOpen: 50, MaxDaily: 30})
	b.now = func() time.Time { return now }
	return b, acc, ws, st
}

func text(s string) Message {
	return Message{ToUserName: "gh_1", FromUserName: "oA", CreateTime: now.Unix(), MsgType: "text", Content: s, MsgID: 1}
}

func ptr[T any](v T) *T { return &v }

func handle(t *testing.T, b *Bot, m Message) string {
	t.Helper()
	r, err := b.Handle(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustContain(t *testing.T, reply string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(reply, p) {
			t.Errorf("reply lacks %q:\n%s", p, reply)
		}
	}
}

// protocol

func TestVerify(t *testing.T) {
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := signature("tok", ts, "n1")
	if !verify("tok", sig, ts, "n1", now) || !verify("tok", strings.ToUpper(sig), ts, "n1", now) {
		t.Fatal("valid signature rejected")
	}
	old := strconv.FormatInt(now.Add(-11*time.Minute).Unix(), 10)
	for name, ok := range map[string]bool{
		"wrong token": verify("other", sig, ts, "n1", now),
		"wrong nonce": verify("tok", sig, ts, "n2", now),
		"stale":       verify("tok", signature("tok", old, "n1"), old, "n1", now),
		"bad time":    verify("tok", signature("tok", "x", "n1"), "x", "n1", now),
		"no token":    verify("", signature("", ts, "n1"), ts, "n1", now),
	} {
		if ok {
			t.Errorf("%s accepted", name)
		}
	}
	// The example from WeChat's documentation shape: sorted, concatenated, sha1.
	if got := signature("b", "c", "a"); got != "a9993e364706816aba3e25717850c26c9cd0d89d" { // sha1("abc")
		t.Errorf("signature = %s", got)
	}
}

func TestParseAndEncode(t *testing.T) {
	m, err := parseMessage([]byte(`<xml><ToUserName><![CDATA[gh_1]]></ToUserName><FromUserName><![CDATA[oA]]></FromUserName>
<CreateTime>1700000000</CreateTime><MsgType><![CDATA[text]]></MsgType><Content><![CDATA[https://mp.weixin.qq.com/s?a=1&b=2]]></Content><MsgId>42</MsgId></xml>`))
	if err != nil || m.MsgType != "text" || m.FromUserName != "oA" || m.MsgID != 42 || m.Content != "https://mp.weixin.qq.com/s?a=1&b=2" {
		t.Fatalf("parsed %+v, %v", m, err)
	}
	if m.dedupKey() != "m42" {
		t.Errorf("key %s", m.dedupKey())
	}
	ev, _ := parseMessage([]byte(`<xml><ToUserName>gh_1</ToUserName><FromUserName>oA</FromUserName><CreateTime>7</CreateTime><MsgType>event</MsgType><Event>Subscribe</Event></xml>`))
	if ev.Event != "subscribe" || ev.dedupKey() != "eoA:7:subscribe" {
		t.Errorf("event %+v key %s", ev, ev.dedupKey())
	}

	b := encodeText("oA", "gh_1", `a]]>b <a href="x">y</a>`, now)
	var back struct {
		ToUserName, FromUserName, MsgType, Content string
		CreateTime                                 int64
	}
	if err := xml.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Content != `a]]>b <a href="x">y</a>` || back.ToUserName != "oA" || back.FromUserName != "gh_1" || back.MsgType != "text" || back.CreateTime != now.Unix() {
		t.Errorf("round trip: %+v from %s", back, b)
	}
}

// bot

func TestUnboundUser(t *testing.T) {
	b, acc, ws, _ := newBot()
	r := handle(t, b, Message{FromUserName: "oA", MsgType: "event", Event: "subscribe"})
	mustContain(t, r, "欢迎关注折光观察者", `<a href="https://ob.test/wx/bind#t=BINDTOKEN">绑定邮箱</a>`, "60 天")

	r = handle(t, b, text("看看这篇 https://mp.weixin.qq.com/s/abcdefgh ，很重要"))
	mustContain(t, r, "绑定后会自动开始观察", "/wx/bind#t=")
	if got := acc.bindLinks[len(acc.bindLinks)-1]; got != "https://mp.weixin.qq.com/s/abcdefgh" {
		t.Errorf("pending link %q", got)
	}
	if len(ws.calls) != 0 || acc.logins != 0 {
		t.Error("an unbound user submitted or got a login link")
	}
	for _, s := range []string{"我的", "绑定", "你好"} {
		mustContain(t, handle(t, b, text(s)), "/wx/bind#t=")
	}
	mustContain(t, handle(t, b, text("解绑")), "还没有绑定")
	mustContain(t, handle(t, b, Message{FromUserName: "oA", MsgType: "image"}), "/wx/bind#t=")
}

func TestBoundSubmit(t *testing.T) {
	b, acc, ws, st := newBot()
	acc.bound["oA"] = db.User{ID: uuid.New(), Email: "alice@example.com", Status: "active", Role: "user"}
	id, art := uuid.New(), uuid.New()
	expires := time.Date(2027, 1, 20, 1, 0, 0, 0, time.UTC)

	ws.sub = watch.Submission{Watch: db.Watch{ID: id, ArticleID: &art, Status: "resolving", ExpiresAt: expires}}
	r := handle(t, b, text("https://mp.weixin.qq.com/s/abcdefgh"))
	mustContain(t, r, "正在备份", `<a href="https://ob.test/wx/go?to=/watches/`+id.String()+`#t=LOGINTOKEN">查看进度</a>`)

	st.article = db.Article{Title: ptr("标题 <b>粗</b>")}
	ws.sub = watch.Submission{Watch: db.Watch{ID: id, ArticleID: &art, Status: "active", ExpiresAt: expires}}
	mustContain(t, handle(t, b, text("https://mp.weixin.qq.com/s/abcdefgh")), "已开始观察《标题 ＜b＞粗＜/b＞》", "2027-01-20", "查看备份")

	ws.sub.Existing = true
	mustContain(t, handle(t, b, text("https://mp.weixin.qq.com/s/abcdefgh")), "已经在观察中")

	st.article = db.Article{DeletedReasonText: ptr("该内容已被发布者删除")}
	ws.sub = watch.Submission{Watch: db.Watch{ID: id, ArticleID: &art, Status: "ended", EndReason: ptr("deleted")}}
	mustContain(t, handle(t, b, text("https://mp.weixin.qq.com/s/abcdefgh")), "这篇文章已经被删除了（页面显示：该内容已被发布者删除）", "查看已有的备份")

	ws.sub = watch.Submission{Watch: db.Watch{ID: id, ArticleID: &art, Status: "failed", EndReason: ptr("invalid")}}
	if r := handle(t, b, text("https://mp.weixin.qq.com/s/abcdefgh")); !strings.Contains(r, "打不开") || strings.Contains(r, "<a") {
		t.Errorf("invalid: %s", r)
	}

	// A link message works like a pasted link.
	ws.sub = watch.Submission{Watch: db.Watch{ID: id, Status: "resolving"}}
	handle(t, b, Message{FromUserName: "oA", MsgType: "link", URL: "https://mp.weixin.qq.com/s/zzzzzzzz"})
	if last := ws.calls[len(ws.calls)-1]; last != "https://mp.weixin.qq.com/s/zzzzzzzz" {
		t.Errorf("link message submitted %q", last)
	}

	for err, want := range map[error]string{
		fmt.Errorf("%w: x", watch.ErrBadLink): "不是有效的",
		watch.ErrOpenLimit:                    "50 篇",
		watch.ErrDailyLimit:                   "30 个",
	} {
		ws.err = err
		mustContain(t, handle(t, b, text("https://mp.weixin.qq.com/s/abcdefgh")), want)
	}
	ws.err = errors.New("db down")
	if _, err := b.Handle(context.Background(), text("https://mp.weixin.qq.com/s/abcdefgh")); err == nil {
		t.Error("an internal error became a reply")
	}
	ws.err = nil

	acc.bound["oA"] = db.User{ID: uuid.New(), Email: "alice@example.com", Status: "disabled"}
	mustContain(t, handle(t, b, text("https://mp.weixin.qq.com/s/abcdefgh")), "已停用")
}

func TestBoundCommands(t *testing.T) {
	b, acc, _, st := newBot()
	acc.bound["oA"] = db.User{ID: uuid.New(), Email: "alice@example.com", Status: "active"}

	mustContain(t, handle(t, b, text("帮助")), "a***@example.com", "发送「我的」", `/wx/go?to=/watches#t=LOGINTOKEN">打开网站</a>`)
	mustContain(t, handle(t, b, text("绑定")), "已绑定 a***@example.com")
	mustContain(t, handle(t, b, text("我的")), "观察中 0 篇", "还没有观察的文章")

	// Many long titles: the list stops within WeChat's limit, links share one token.
	st.open = 40
	for i := range 5 {
		st.rows = append(st.rows, db.ListWatchesPageRow{ID: uuid.New(), Status: "active", ExpiresAt: now.Add(time.Duration(i*3) * 24 * time.Hour),
			Title: ptr(strings.Repeat("很长的标题", 20))})
	}
	st.rows[0].Status, st.rows[1].Status, st.rows[1].EndReason = "resolving", "ended", ptr("deleted")
	acc.logins = 0
	r := handle(t, b, text("我的"))
	mustContain(t, r, "观察中 40 篇", "备份中", "已删除", "即将到期", "全部观察")
	if len(r) > maxReplyBytes {
		t.Errorf("reply is %d bytes", len(r))
	}
	if acc.logins != 1 {
		t.Errorf("%d login tokens for one reply", acc.logins)
	}

	mustContain(t, handle(t, b, text("解绑")), "已解除与 a***@example.com 的绑定")
	if _, ok := acc.bound["oA"]; ok {
		t.Error("still bound")
	}
}

func TestUnsubscribeForgets(t *testing.T) {
	b, acc, _, _ := newBot()
	acc.bound["oA"] = db.User{ID: uuid.New(), Email: "alice@example.com", Status: "active"}
	if r := handle(t, b, Message{FromUserName: "oA", MsgType: "event", Event: "unsubscribe"}); r != "" {
		t.Errorf("reply to unsubscribe: %q", r)
	}
	if len(acc.forgot) != 1 || len(acc.bound) != 0 {
		t.Errorf("forgot %v, bound %v", acc.forgot, acc.bound)
	}
	if r := handle(t, b, Message{FromUserName: "oA", MsgType: "event", Event: "click"}); r != "" {
		t.Errorf("reply to a click: %q", r)
	}
}

func TestBind(t *testing.T) {
	b, acc, ws, _ := newBot()
	u := db.User{ID: uuid.New(), Role: "user"}
	if r, err := b.Bind(context.Background(), u, "t"); err != nil || r.WatchID != nil || len(ws.calls) != 0 {
		t.Fatalf("no pending link: %+v %v", r, err)
	}
	acc.pending = "https://mp.weixin.qq.com/s/abcdefgh"
	id := uuid.New()
	ws.sub = watch.Submission{Watch: db.Watch{ID: id}}
	if r, err := b.Bind(context.Background(), u, "t"); err != nil || r.WatchID == nil || *r.WatchID != id {
		t.Fatalf("pending link: %+v %v", r, err)
	}
	ws.err = watch.ErrOpenLimit
	if r, err := b.Bind(context.Background(), u, "t"); err != nil || !strings.Contains(r.SubmitError, "上限") {
		t.Fatalf("quota: %+v %v", r, err)
	}
	acc.bindErr = auth.ErrWechatLink
	if _, err := b.Bind(context.Background(), u, "t"); !errors.Is(err, auth.ErrWechatLink) {
		t.Fatalf("bad link: %v", err)
	}
}

func TestHelpers(t *testing.T) {
	for in, want := range map[string]string{"alice@example.com": "a***@example.com", "张三@例子.cn": "张***@例子.cn", "@x": "***", "x": "***"} {
		if got := maskEmail(in); got != want {
			t.Errorf("maskEmail(%q) = %q", in, got)
		}
	}
	if got := clip("一二三四五六", 4); got != "一二三…" {
		t.Errorf("clip = %q", got)
	}
	if got := clip(" a\n b ", 10); got != "a b" {
		t.Errorf("clip spaces = %q", got)
	}
}

// handler

func newHandler(t *testing.T) (*Handler, *fakeAccounts, *Bot) {
	t.Helper()
	b, acc, _, _ := newBot()
	h := NewHandler(b, "tok", "gh_1", slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.now = func() time.Time { return now }
	return h, acc, b
}

func signedURL(token string, extra url.Values) string {
	ts := strconv.FormatInt(now.Unix(), 10)
	v := url.Values{"timestamp": {ts}, "nonce": {"n"}, "signature": {signature(token, ts, "n")}, "openid": {"oA"}}
	for k, vs := range extra {
		v[k] = vs
	}
	return Path + "?" + v.Encode()
}

func serve(h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec
}

func msgXML(from, to, content string, id int) string {
	return fmt.Sprintf(`<xml><ToUserName><![CDATA[%s]]></ToUserName><FromUserName><![CDATA[%s]]></FromUserName><CreateTime>%d</CreateTime><MsgType><![CDATA[text]]></MsgType><Content><![CDATA[%s]]></Content><MsgId>%d</MsgId></xml>`,
		to, from, now.Unix(), content, id)
}

func TestHandlerVerifiesURL(t *testing.T) {
	h, _, _ := newHandler(t)
	if rec := serve(h, http.MethodGet, signedURL("tok", url.Values{"echostr": {"12345"}}), ""); rec.Code != 200 || rec.Body.String() != "12345" {
		t.Errorf("echo: %d %q", rec.Code, rec.Body)
	}
	if rec := serve(h, http.MethodGet, signedURL("wrong", url.Values{"echostr": {"12345"}}), ""); rec.Code != http.StatusForbidden {
		t.Errorf("bad signature: %d", rec.Code)
	}
	if rec := serve(h, http.MethodPost, Path, msgXML("oA", "gh_1", "hi", 1)); rec.Code != http.StatusForbidden {
		t.Errorf("unsigned post: %d", rec.Code)
	}
	if rec := serve(h, http.MethodPut, signedURL("tok", nil), ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("put: %d", rec.Code)
	}
	if rec := serve(h, http.MethodPost, signedURL("tok", nil), "not xml"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad body: %d", rec.Code)
	}
	if rec := serve(h, http.MethodPost, signedURL("tok", nil), msgXML("oA", "gh_other", "hi", 1)); rec.Code != http.StatusBadRequest {
		t.Errorf("other account: %d", rec.Code)
	}
	if rec := serve(h, http.MethodPost, signedURL("tok", nil), strings.Repeat("x", maxBody+1)); rec.Code != http.StatusBadRequest {
		t.Errorf("huge body: %d", rec.Code)
	}
}

func replyContent(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var r struct{ Content, ToUserName, FromUserName string }
	if err := xml.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("reply %q: %v", rec.Body, err)
	}
	if r.ToUserName != "oA" || r.FromUserName != "gh_1" {
		t.Errorf("addressed %s → %s", r.FromUserName, r.ToUserName)
	}
	return r.Content
}

func TestHandlerReplies(t *testing.T) {
	h, acc, _ := newHandler(t)
	rec := serve(h, http.MethodPost, signedURL("tok", nil), msgXML("oA", "gh_1", "https://mp.weixin.qq.com/s/abcdefgh", 1))
	mustContain(t, replyContent(t, rec), "/wx/bind#t=BINDTOKEN")
	// WeChat's retry gets the same reply without doing it again.
	rec = serve(h, http.MethodPost, signedURL("tok", nil), msgXML("oA", "gh_1", "https://mp.weixin.qq.com/s/abcdefgh", 1))
	mustContain(t, replyContent(t, rec), "/wx/bind#t=BINDTOKEN")
	if len(acc.bindLinks) != 1 {
		t.Errorf("%d bind links for one message", len(acc.bindLinks))
	}
	// No reply: "success".
	unsub := `<xml><ToUserName>gh_1</ToUserName><FromUserName>oA</FromUserName><CreateTime>1</CreateTime><MsgType>event</MsgType><Event>unsubscribe</Event></xml>`
	if rec := serve(h, http.MethodPost, signedURL("tok", nil), unsub); rec.Body.String() != "success" {
		t.Errorf("unsubscribe reply %q", rec.Body)
	}
}

func TestHandlerRateLimit(t *testing.T) {
	h, acc, _ := newHandler(t)
	for i := range userBurst {
		serve(h, http.MethodPost, signedURL("tok", nil), msgXML("oA", "gh_1", "hi", 100+i))
	}
	rec := serve(h, http.MethodPost, signedURL("tok", nil), msgXML("oA", "gh_1", "hi", 99))
	if got := replyContent(t, rec); got != tooManyText {
		t.Errorf("over the limit: %q", got)
	}
	if len(acc.bindLinks) != userBurst {
		t.Errorf("%d messages handled", len(acc.bindLinks))
	}
	// Another user is not affected.
	rec = serve(h, http.MethodPost, signedURL("tok", nil), msgXML("oB", "gh_1", "hi", 98))
	var r struct{ Content string }
	_ = xml.Unmarshal(rec.Body.Bytes(), &r)
	if r.Content == tooManyText {
		t.Error("other user limited")
	}
	// Refilled later; idle users are forgotten.
	h.now = func() time.Time { return now.Add(userBurst*userEvery + 2*time.Minute) }
	if !h.allow("oA") {
		t.Error("not refilled")
	}
	h.now = func() time.Time { return now.Add(2 * (userBurst*userEvery + 2*time.Minute)) }
	h.allow("oC")
	if _, ok := h.users["oA"]; ok {
		t.Error("idle user not swept")
	}
}

func TestPseudonym(t *testing.T) {
	if p := pseudonym("oA"); len(p) != 8 || strings.Contains(p, "oA") || p != pseudonym("oA") || p == pseudonym("oB") {
		t.Errorf("pseudonym %q", p)
	}
}

func TestSimulate(t *testing.T) {
	h, acc, _ := newHandler(t)
	h.now = time.Now // Simulate signs with the real clock
	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx := context.Background()
	r, err := Simulate(ctx, srv.Client(), srv.URL, "tok", Message{ToUserName: "gh_1", FromUserName: "oA", MsgType: "text", Content: "帮助"})
	if err != nil || !strings.Contains(r, "/wx/bind#t=") {
		t.Fatalf("text: %q %v", r, err)
	}
	acc.bound["oA"] = db.User{ID: uuid.New(), Email: "a@example.com"}
	if r, err := Simulate(ctx, srv.Client(), srv.URL, "tok", Message{ToUserName: "gh_1", FromUserName: "oA", MsgType: "event", Event: "unsubscribe"}); err != nil || r != "" {
		t.Fatalf("event: %q %v", r, err)
	}
	if len(acc.bound) != 0 {
		t.Error("unsubscribe not handled")
	}
	if _, err := Simulate(ctx, srv.Client(), srv.URL, "wrong", Message{ToUserName: "gh_1", FromUserName: "oA", MsgType: "text"}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("wrong token: %v", err)
	}
}
