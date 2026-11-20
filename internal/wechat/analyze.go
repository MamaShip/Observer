package wechat

import (
	"bytes"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
)

// Verdict is the outcome of one check.
type Verdict string

const (
	VerdictAlive   Verdict = "alive"   // the article is readable
	VerdictDeleted Verdict = "deleted" // WeChat says the article is gone
	VerdictInvalid Verdict = "invalid" // the link itself is wrong or expired
	VerdictBlocked Verdict = "blocked" // verification page or rate limit: no conclusion
	VerdictError   Verdict = "error"   // unexpected response: no conclusion
)

// Conclusive reports whether the verdict says something about the article.
func (v Verdict) Conclusive() bool {
	return v == VerdictAlive || v == VerdictDeleted || v == VerdictInvalid
}

// Reason classifies why an article is gone.
type Reason string

const (
	ReasonAuthorDeleted  Reason = "author_deleted"
	ReasonViolation      Reason = "violation"
	ReasonComplaint      Reason = "complaint"
	ReasonAccountBlocked Reason = "account_blocked"
	ReasonUnknown        Reason = "unknown"
)

// Result is what Analyze concludes about a fetched page.
type Result struct {
	Verdict Verdict
	Reason  Reason // set when Verdict is VerdictDeleted
	Message string // the headline WeChat shows, if any
	Detail  string // the explanation WeChat shows, if any
	Rule    string // which rule decided, for debugging and alerts
	PageMID string // WeChat's page template id, e.g. "mmbizwap:appmsg/newindex.html"
	Meta    *Meta  // set when Verdict is VerdictAlive
}

// Meta is article metadata read from an article page.
type Meta struct {
	Biz, Mid, Idx, Sn string
	MsgLink           string // WeChat's own share link, usually the short form
	Title             string
	AccountName       string // display name, e.g. 健康成都官微
	AccountID         string // gh_xxx
	Author            string
	Description       string
	CoverURL          string
	PublishedAt       time.Time // zero when not in the (possibly partial) body
	ItemShowType      string    // "0" is a normal article; other values are image/video/text posts
	ContentRunes      int
	ImageURLs         []string
}

// CanonicalKey returns biz:mid:idx, or "" if any part is missing.
func (m *Meta) CanonicalKey() string { return CanonicalKey(m.Biz, m.Mid, m.Idx) }

var (
	rePageMID = regexp.MustCompile(`PAGE_MID\s*=\s*['"]([^'"]+)['"]`)

	verifyMarkers   = []string{"secitptpage/verify", "wappoc_appmsgcaptcha", "/mp/verify"}
	blockedWords    = []string{"环境异常", "完成验证", "去验证", "访问过于频繁", "操作频繁", "稍后再试"}
	invalidWords    = []string{"参数错误", "链接已过期", "链接已失效", "页面不存在", "内容不存在", "无效的链接"}
	authorWords     = []string{"发布者删除", "作者删除", "已被发布者"}
	complaintWords  = []string{"侵权", "投诉"}
	violationWords  = []string{"违规", "违反"}
	accountWords    = []string{"封禁", "屏蔽", "迁移", "注销", "冻结", "帐号", "账号"}
	genericGoneWord = []string{"删除", "无法查看", "已停止访问", "不可查看"}
)

// Analyze classifies a fetched page. It never treats an unrecognized page
// as alive: when no rule matches, the verdict is VerdictError.
func Analyze(status int, finalURL string, body []byte) Result {
	return analyze(status, finalURL, body, false)
}

// AnalyzeHead classifies a page fetched with FetchHead. Besides the rules
// of Analyze, it accepts a head with og:type=article, og:title and og:url
// as alive. Experimental: it relies on deleted, blocked and invalid pages
// never carrying article Open Graph tags, which holds for every sample so
// far. (The template id PAGE_MID is only assigned ~0.7 MB into the page.)
func AnalyzeHead(status int, finalURL string, body []byte) Result {
	return analyze(status, finalURL, body, true)
}

func analyze(status int, finalURL string, body []byte, headOnly bool) Result {
	r := Result{}
	if m := rePageMID.FindSubmatch(body); m != nil {
		r.PageMID = string(m[1])
	}

	switch {
	case status == 429:
		return r.with(VerdictBlocked, "http_429")
	case status != 200:
		r.Message = strconv.Itoa(status)
		return r.with(VerdictError, "http_status")
	}
	for _, mk := range verifyMarkers {
		if strings.Contains(r.PageMID, mk) || strings.Contains(finalURL, mk) {
			return r.with(VerdictBlocked, "verify_page")
		}
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		r.Message = err.Error()
		return r.with(VerdictError, "parse_error")
	}
	isArticleTemplate := strings.HasPrefix(r.PageMID, "mmbizwap:appmsg/")

	if content := doc.Find("#js_content").First(); content.Length() > 0 {
		meta := extractMeta(doc, body)
		meta.ContentRunes = utf8.RuneCountInString(strings.TrimSpace(content.Text()))
		meta.ImageURLs = imageURLs(content)
		r.Meta = meta
		if meta.ContentRunes > 0 || len(meta.ImageURLs) > 0 || content.Find("iframe,video,mpvoice,mp-common-videosnap").Length() > 0 {
			return r.with(VerdictAlive, "content_node")
		}
		if isArticleTemplate && meta.Title != "" {
			return r.with(VerdictAlive, "article_template_empty_content")
		}
		r.Meta = nil
		return r.with(VerdictError, "empty_content")
	}

	if title := collapse(doc.Find(".weui-msg__title").First().Text()); title != "" {
		r.Message = title
		r.Detail = collapse(doc.Find(".weui-msg__desc").First().Text())
		return classifyMessage(r)
	}

	if isArticleTemplate {
		// Image, video or text posts may not use #js_content. Accept only
		// with a title; new layouts must get fixtures before being trusted.
		meta := extractMeta(doc, body)
		if meta.Title != "" && meta.CanonicalKey() != "" {
			r.Meta = meta
			return r.with(VerdictAlive, "article_template_no_content_node")
		}
	}
	if headOnly {
		og := func(p string) string {
			return strings.TrimSpace(doc.Find(`meta[property="og:`+p+`"]`).AttrOr("content", ""))
		}
		if og("type") == "article" && og("title") != "" && strings.Contains(og("url"), ArticleHost) {
			r.Meta = extractMeta(doc, body)
			return r.with(VerdictAlive, "og_article_head")
		}
	}
	if containsAny(string(body), blockedWords[:2]) {
		return r.with(VerdictBlocked, "verify_text")
	}
	return r.with(VerdictError, "unrecognized_page")
}

func classifyMessage(r Result) Result {
	t := r.Message
	switch {
	case containsAny(t, blockedWords):
		return r.with(VerdictBlocked, "message_blocked")
	case containsAny(t, invalidWords):
		return r.with(VerdictInvalid, "message_invalid")
	case containsAny(t, authorWords):
		r.Reason = ReasonAuthorDeleted
	case containsAny(t, complaintWords) && !containsAny(t, violationWords):
		r.Reason = ReasonComplaint
	case containsAny(t, violationWords):
		r.Reason = ReasonViolation
	case containsAny(t, accountWords):
		r.Reason = ReasonAccountBlocked
	case containsAny(t, genericGoneWord):
		r.Reason = ReasonUnknown
	default:
		// An unknown message is surfaced as an error, not guessed as a deletion.
		return r.with(VerdictError, "unrecognized_message")
	}
	return r.with(VerdictDeleted, "message_deleted")
}

func (r Result) with(v Verdict, rule string) Result {
	r.Verdict, r.Rule = v, rule
	return r
}

func extractMeta(doc *goquery.Document, body []byte) *Meta {
	m := &Meta{
		Biz:          scriptVar(body, "biz"),
		Mid:          scriptVar(body, "mid"),
		Idx:          scriptVar(body, "idx"),
		Sn:           scriptVar(body, "sn"),
		MsgLink:      jsUnescape(scriptVar(body, "msg_link")),
		AccountID:    scriptVar(body, "user_name"),
		ItemShowType: scriptVar(body, "item_show_type"),
	}
	metaContent := func(sel string) string {
		v, _ := doc.Find(sel).First().Attr("content")
		return strings.TrimSpace(v)
	}
	m.Title = firstNonEmpty(metaContent(`meta[property="og:title"]`),
		collapse(doc.Find("#activity-name").First().Text()),
		html.UnescapeString(scriptVar(body, "msg_title")))
	m.AccountName = firstNonEmpty(collapse(doc.Find("#js_name").First().Text()),
		html.UnescapeString(scriptVar(body, "nickname")))
	m.Author = firstNonEmpty(metaContent(`meta[name="author"]`), html.UnescapeString(scriptVar(body, "author")))
	m.Description = metaContent(`meta[property="og:description"]`)
	m.CoverURL = metaContent(`meta[property="og:image"]`)
	if ct := firstNonEmpty(scriptVar(body, "ct"), scriptVar(body, "create_time")); ct != "" {
		if sec, err := strconv.ParseInt(ct, 10, 64); err == nil && sec > 0 {
			m.PublishedAt = time.Unix(sec, 0).UTC()
		}
	}
	// Fall back to the identifiers inside WeChat's own links.
	for _, link := range []string{m.MsgLink, metaContent(`meta[property="og:url"]`)} {
		if m.CanonicalKey() != "" && m.Sn != "" {
			break
		}
		if u, err := url.Parse(link); err == nil {
			q := u.Query()
			m.Biz = firstNonEmpty(m.Biz, q.Get("__biz"))
			m.Mid = firstNonEmpty(m.Mid, q.Get("mid"))
			m.Idx = firstNonEmpty(m.Idx, q.Get("idx"))
			m.Sn = firstNonEmpty(m.Sn, q.Get("sn"))
		}
	}
	return m
}

func imageURLs(content *goquery.Selection) []string {
	seen := map[string]bool{}
	var out []string
	content.Find("img").Each(func(_ int, img *goquery.Selection) {
		src := img.AttrOr("data-src", "")
		if src == "" {
			src = img.AttrOr("src", "")
		}
		u, err := url.Parse(strings.TrimSpace(src))
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !IsAssetHost(u.Hostname()) {
			return
		}
		if s := u.String(); !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	})
	return out
}

// scriptVar finds `var name = "literal" [|| "literal"...]` (optionally
// wrapped in htmlDecode(...)) and returns the first non-empty literal.
func scriptVar(body []byte, name string) string {
	scriptVarMu.Lock()
	re := scriptVarRes[name]
	if re == nil {
		re = regexp.MustCompile(`\bvar\s+` + regexp.QuoteMeta(name) +
			`\s*=\s*(?:htmlDecode\(\s*)?((?:"[^"\n]*"|'[^'\n]*')(?:\s*\|\|\s*(?:"[^"\n]*"|'[^'\n]*'))*)`)
		scriptVarRes[name] = re
	}
	scriptVarMu.Unlock()
	for _, m := range re.FindAllSubmatch(body, 8) {
		for _, lit := range reLiteral.FindAll(m[1], -1) {
			if v := string(lit[1 : len(lit)-1]); v != "" {
				return v
			}
		}
	}
	return ""
}

var (
	scriptVarMu  sync.Mutex
	scriptVarRes = map[string]*regexp.Regexp{}
	reLiteral    = regexp.MustCompile(`"[^"\n]*"|'[^'\n]*'`)
)

func jsUnescape(s string) string {
	s = strings.ReplaceAll(s, `\x26`, "&")
	s = strings.ReplaceAll(s, `\/`, "/")
	return html.UnescapeString(s)
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
