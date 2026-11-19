package wechat

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAnalyzeFixtures(t *testing.T) {
	tests := []struct {
		file        string
		finalURL    string
		wantVerdict Verdict
		wantReason  Reason
		wantMessage string
	}{
		{file: "alive_article.html", wantVerdict: VerdictAlive},
		{file: "deleted_by_author.html", wantVerdict: VerdictDeleted, wantReason: ReasonAuthorDeleted, wantMessage: "该内容已被发布者删除"},
		{file: "deleted_violation.html", wantVerdict: VerdictDeleted, wantReason: ReasonViolation, wantMessage: "此内容因违规无法查看"},
		{file: "blocked_verify.html", wantVerdict: VerdictBlocked},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			r := Analyze(200, "https://mp.weixin.qq.com/s/x", fixture(t, tt.file))
			if r.Verdict != tt.wantVerdict || r.Reason != tt.wantReason || r.Message != tt.wantMessage {
				t.Fatalf("got verdict=%s reason=%s message=%q rule=%s, want %s/%s/%q",
					r.Verdict, r.Reason, r.Message, r.Rule, tt.wantVerdict, tt.wantReason, tt.wantMessage)
			}
		})
	}
}

func TestAnalyzeAliveMeta(t *testing.T) {
	r := Analyze(200, "", fixture(t, "alive_article.html"))
	m := r.Meta
	if m == nil {
		t.Fatalf("no meta, rule=%s", r.Rule)
	}
	checks := map[string][2]string{
		"key":       {m.CanonicalKey(), "MzA3Mjk4NDU3MQ==:2652612955:1"},
		"sn":        {m.Sn, "c82b5aaf0462dcc9212bc4d6089dad4c"},
		"msg_link":  {m.MsgLink, "https://mp.weixin.qq.com/s/q-WkvTApjcwqtgk9LY7Q-A"},
		"account":   {m.AccountName, "健康成都官微"},
		"accountID": {m.AccountID, "gh_e673ac561851"},
		"showType":  {m.ItemShowType, "0"},
	}
	for k, v := range checks {
		if v[0] != v[1] {
			t.Errorf("%s = %q, want %q", k, v[0], v[1])
		}
	}
	if !strings.HasPrefix(m.Title, "我市新型冠状病毒肺炎疫情最新情况") {
		t.Errorf("title = %q", m.Title)
	}
	if want := time.Unix(1590541039, 0).UTC(); !m.PublishedAt.Equal(want) {
		t.Errorf("published = %v, want %v", m.PublishedAt, want)
	}
	if m.ContentRunes < 100 || len(m.ImageURLs) == 0 {
		t.Errorf("content runes=%d images=%d", m.ContentRunes, len(m.ImageURLs))
	}
	for _, u := range m.ImageURLs {
		if !strings.HasPrefix(u, "https://mmbiz.qpic.cn/") {
			t.Errorf("unexpected image url %s", u)
		}
	}
}

// A check fetch stops shortly after the content marker; the verdict must
// not depend on the rest of the page.
func TestAnalyzeTruncatedAlive(t *testing.T) {
	body := fixture(t, "alive_article.html")
	i := bytes.Index(body, contentMarker)
	if i < 0 {
		t.Fatal("marker missing")
	}
	cut := body[:min(len(body), i+4096)]
	if r := Analyze(200, "", cut); r.Verdict != VerdictAlive {
		t.Fatalf("verdict = %s rule=%s", r.Verdict, r.Rule)
	}
}

func TestAnalyzeSynthetic(t *testing.T) {
	page := func(pageMID, body string) []byte {
		return []byte(`<html><head><script>window.PAGE_MID="` + pageMID + `";</script></head><body>` + body + `</body></html>`)
	}
	msg := func(title, desc string) []byte {
		return page("", `<div class="weui-msg"><h2 class="weui-msg__title">`+title+`</h2><div class="weui-msg__desc">`+desc+`</div></div>`)
	}
	tests := []struct {
		name        string
		status      int
		finalURL    string
		body        []byte
		wantVerdict Verdict
		wantReason  Reason
	}{
		{"complaint", 200, "", msg("此内容被投诉且经审核涉嫌侵权，无法查看", ""), VerdictDeleted, ReasonComplaint},
		{"violation title wins over complaint desc", 200, "", msg("此内容因违规无法查看", "接相关投诉，此内容违反规定"), VerdictDeleted, ReasonViolation},
		{"account", 200, "", msg("此帐号已被屏蔽, 内容无法查看", ""), VerdictDeleted, ReasonAccountBlocked},
		{"migrated", 200, "", msg("该公众号已迁移", ""), VerdictDeleted, ReasonAccountBlocked},
		{"generic deletion", 200, "", msg("该内容已被删除", ""), VerdictDeleted, ReasonUnknown},
		{"rate limited message is not a deletion", 200, "", msg("访问过于频繁，请稍后再试", ""), VerdictBlocked, ""},
		{"invalid link", 200, "", msg("参数错误", ""), VerdictInvalid, ""},
		{"expired link", 200, "", msg("链接已过期", ""), VerdictInvalid, ""},
		{"unknown message is an error, not a deletion", 200, "", msg("系统升级中", ""), VerdictError, ""},
		{"captcha by url", 200, "https://mp.weixin.qq.com/mp/wappoc_appmsgcaptcha?x=1", page("", ""), VerdictBlocked, ""},
		{"captcha by text", 200, "", page("", `<p>当前环境异常，完成验证后即可继续访问。</p>`), VerdictBlocked, ""},
		{"http 429", 429, "", nil, VerdictBlocked, ""},
		{"http 500", 500, "", nil, VerdictError, ""},
		{"empty page", 200, "", page("", ""), VerdictError, ""},
		{"empty content node outside article template", 200, "", page("", `<div id="js_content"></div>`), VerdictError, ""},
		{"article words inside alive content do not block", 200, "",
			page("mmbizwap:appmsg/newindex.html", `<div id="js_content"><p>文章讨论环境异常和去验证的话题</p></div>`), VerdictAlive, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Analyze(tt.status, tt.finalURL, tt.body)
			if r.Verdict != tt.wantVerdict || r.Reason != tt.wantReason {
				t.Fatalf("got %s/%s (rule %s), want %s/%s", r.Verdict, r.Reason, r.Rule, tt.wantVerdict, tt.wantReason)
			}
		})
	}
}

func TestScriptVar(t *testing.T) {
	body := []byte(`var sn={deltaE76:function(){}};
var biz = "" || "MzA==";
var nickname = htmlDecode("A&amp;B");
var mid = opt.mid || window.mid;
var mid = "123" || "";
var title = 'single'.html(false);`)
	for name, want := range map[string]string{"biz": "MzA==", "nickname": "A&amp;B", "mid": "123", "sn": "", "title": "single"} {
		if got := scriptVar(body, name); got != want {
			t.Errorf("scriptVar(%s) = %q, want %q", name, got, want)
		}
	}
}

func TestAnalyzeHead(t *testing.T) {
	body := fixture(t, "alive_article.html")
	i := bytes.Index(body, headMarker)
	if i < 0 {
		t.Fatal("og:type missing")
	}
	head := body[:i+1024]

	if r := AnalyzeHead(200, "", head); r.Verdict != VerdictAlive || r.Rule != "og_article_head" || r.Meta.Title == "" {
		t.Fatalf("head: %s rule=%s", r.Verdict, r.Rule)
	}
	// The same partial body is not enough for the strict analysis.
	if r := Analyze(200, "", head); r.Verdict == VerdictAlive {
		t.Fatalf("strict analysis accepted a head-only body (rule=%s)", r.Rule)
	}
	// og:type must be article.
	notArticle := bytes.Replace(head, []byte(`property="og:type" content="article"`), []byte(`property="og:type" content="website"`), 1)
	if r := AnalyzeHead(200, "", notArticle); r.Verdict == VerdictAlive {
		t.Fatalf("non-article head accepted (rule=%s)", r.Rule)
	}
	for file, want := range map[string]Verdict{
		"deleted_by_author.html": VerdictDeleted, "deleted_violation.html": VerdictDeleted, "blocked_verify.html": VerdictBlocked,
	} {
		if r := AnalyzeHead(200, "", fixture(t, file)); r.Verdict != want {
			t.Errorf("%s: %s, want %s", file, r.Verdict, want)
		}
	}
}
