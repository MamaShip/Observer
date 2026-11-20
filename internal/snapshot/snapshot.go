// Package snapshot turns a fetched WeChat article page into a self-contained
// reading page: images stored by content hash and referenced as /a/<sha256>,
// markup and inline styles sanitized, wrapped in a template with print
// styles. Storing images is left to the caller (see AssetFunc), so this
// package does no I/O of its own.
package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/microcosm-cc/bluemonday"

	"github.com/MamaShip/Observer/internal/wechat"
)

// ErrNoContent means the page has no article body to back up.
var ErrNoContent = errors.New("snapshot: page has no #js_content")

// MaxAssets caps images per snapshot; the rest count as missing.
const MaxAssets = 300

// Source describes the article for the page header.
type Source struct {
	URL         string // original article link shown to readers
	Title       string
	AccountName string
	Author      string
	PublishedAt time.Time // zero if unknown
	CapturedAt  time.Time
}

// AssetRef identifies a stored image.
type AssetRef struct {
	SHA256      [32]byte
	ContentType string
}

// AssetFunc downloads and stores one image, returning its reference. An
// error marks the image missing; the snapshot is still produced.
type AssetFunc func(ctx context.Context, url string) (AssetRef, error)

// UsedAsset is an image the snapshot references.
type UsedAsset struct {
	AssetRef
	SourceURL string
}

// Result is a built snapshot.
type Result struct {
	HTML        []byte
	Assets      []UsedAsset
	Missing     int      // images that could not be stored
	ContentHash [32]byte // sha256 of the normalized plain text
	TextRunes   int
}

// Build makes a snapshot from a full article page.
func Build(ctx context.Context, raw []byte, src Source, store AssetFunc) (*Result, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	content := doc.Find("#js_content").First()
	if content.Length() == 0 {
		return nil, ErrNoContent
	}

	replaceMedia(content)
	content.Find("script, style, noscript, link, meta, template").Remove()

	// Phase 1: collect image URLs in document order.
	var urls []string
	seen := map[string]bool{}
	add := func(raw string) {
		if u, ok := assetURL(raw); ok && !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	content.Find("img").Each(func(_ int, img *goquery.Selection) { add(imgSource(img)) })
	content.Find("[style]").Each(func(_ int, el *goquery.Selection) {
		for _, m := range cssURL.FindAllStringSubmatch(el.AttrOr("style", ""), -1) {
			add(unquoteCSS(m[1]))
		}
	})

	// Phase 2: store them.
	res := &Result{}
	stored := map[string]string{} // source URL -> "/a/<hex>"
	for i, u := range urls {
		if i >= MaxAssets {
			res.Missing += len(urls) - MaxAssets
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ref, err := store(ctx, u)
		if err != nil {
			res.Missing++
			continue
		}
		stored[u] = "/a/" + hex.EncodeToString(ref.SHA256[:])
		res.Assets = append(res.Assets, UsedAsset{AssetRef: ref, SourceURL: u})
	}
	lookup := func(raw string) (string, bool) {
		u, ok := assetURL(raw)
		if !ok {
			return "", false
		}
		p, ok := stored[u]
		return p, ok
	}

	// Phase 3: rewrite references and clean styles.
	content.Find("img").Each(func(_ int, img *goquery.Selection) {
		src := imgSource(img)
		for _, a := range []string{"src", "data-src", "srcset", "data-srcset"} {
			img.RemoveAttr(a)
		}
		if p, ok := lookup(src); ok {
			img.SetAttr("src", p)
		} else if strings.HasPrefix(src, "data:image/") {
			img.SetAttr("src", src)
		} else {
			img.SetAttr("alt", "[图片未能备份]")
			img.SetAttr("class", "obs-missing")
		}
	})
	content.Find("[style]").Each(func(_ int, el *goquery.Selection) {
		if s := cleanStyle(el.AttrOr("style", ""), lookup); s != "" {
			el.SetAttr("style", s)
		} else {
			el.RemoveAttr("style")
		}
	})

	text := strings.Join(strings.Fields(content.Text()), " ")
	res.ContentHash = sha256.Sum256([]byte(text))
	res.TextRunes = len([]rune(text))

	body, err := content.Html()
	if err != nil {
		return nil, err
	}
	clean := policy.Sanitize(body)

	var out bytes.Buffer
	err = pageTemplate.Execute(&out, pageData{
		Source:    src,
		Published: formatTime(src.PublishedAt),
		Captured:  formatTime(src.CapturedAt),
		Body:      template.HTML(clean), //nolint:gosec // sanitized by bluemonday above
		Missing:   res.Missing,
	})
	if err != nil {
		return nil, err
	}
	res.HTML = out.Bytes()
	return res, nil
}

// imgSource prefers data-src: WeChat lazy-loads, and src is often a placeholder.
func imgSource(img *goquery.Selection) string {
	if s := strings.TrimSpace(img.AttrOr("data-src", "")); s != "" {
		return s
	}
	return strings.TrimSpace(img.AttrOr("src", ""))
}

// assetURL normalizes an image reference to the https URL to download, or
// reports false if it is not an image on WeChat's CDN.
func assetURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !wechat.IsAssetHost(u.Hostname()) {
		return "", false
	}
	u.Scheme = "https"
	u.Fragment = ""
	return u.String(), true
}

// Media embeds cannot be backed up; leave a visible note in their place.
var mediaSelectors = map[string]string{
	"iframe":                                 "视频",
	"video":                                  "视频",
	"mp-common-videosnap":                    "视频号内容",
	"mpvideosnap":                            "视频号内容",
	"audio":                                  "音频",
	"mpvoice":                                "音频",
	"mp-common-mpaudio":                      "音频",
	"qqmusic":                                "音乐",
	"mp-common-profile, mpprofile":           "公众号名片",
	"mp-miniprogram, .weapp_display_element": "小程序",
}

func replaceMedia(content *goquery.Selection) {
	for sel, kind := range mediaSelectors {
		content.Find(sel).Each(func(_ int, s *goquery.Selection) {
			s.ReplaceWithHtml(`<div class="obs-media">[` + kind + `] 备份不包含此内容，请在原文中查看。</div>`)
		})
	}
}

var cssURL = regexp.MustCompile(`(?i)url\(\s*([^)]*?)\s*\)`)

func unquoteCSS(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "&quot;", `"`)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
	}
	return strings.TrimSpace(s)
}

var dataImage = regexp.MustCompile(`^data:image/(png|gif|jpeg|webp|svg\+xml)[;,]`)

// cleanStyle keeps an inline style's declarations except ones that hide
// content (WeChat reveals it with scripts we don't run), escape the page
// flow, or load anything but our stored images.
func cleanStyle(style string, lookup func(string) (string, bool)) string {
	var keep []string
	for _, decl := range splitDecls(style) {
		name, value, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.TrimSpace(value)
		lv := strings.ToLower(value)
		switch {
		case name == "" || value == "",
			strings.ContainsAny(value, `\<>`),
			strings.Contains(lv, "expression("), strings.Contains(lv, "javascript:"),
			name == "behavior", name == "-moz-binding",
			name == "visibility" && strings.HasPrefix(lv, "hidden"),
			name == "opacity" && strings.TrimSpace(strings.TrimSuffix(lv, "!important")) == "0",
			name == "position" && (strings.HasPrefix(lv, "fixed") || strings.HasPrefix(lv, "sticky")):
			continue
		}
		if strings.Contains(lv, "url(") {
			allOK := true
			value = cssURL.ReplaceAllStringFunc(value, func(m string) string {
				arg := unquoteCSS(cssURL.FindStringSubmatch(m)[1])
				if p, ok := lookup(arg); ok {
					return "url('" + p + "')"
				}
				if dataImage.MatchString(arg) && !strings.ContainsAny(arg, `'"`) {
					return "url('" + arg + "')"
				}
				allOK = false
				return m
			})
			if !allOK {
				continue
			}
		}
		keep = append(keep, name+": "+value)
	}
	return strings.Join(keep, "; ")
}

// splitDecls splits on ';' outside parentheses and quotes (data URIs contain ';').
func splitDecls(s string) []string {
	var out []string
	depth, quote, start := 0, byte(0), 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			if depth > 0 {
				depth--
			}
		case c == ';' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

var policy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("section", "div", "p", "span", "br", "hr", "center", "font",
		"strong", "b", "em", "i", "u", "s", "strike", "del", "ins", "sub", "sup", "small", "big", "mark",
		"blockquote", "pre", "code", "h1", "h2", "h3", "h4", "h5", "h6",
		"ul", "ol", "li", "dl", "dt", "dd", "figure", "figcaption",
		"table", "thead", "tbody", "tfoot", "tr", "td", "th", "caption", "colgroup", "col")
	// Styles were cleaned by cleanStyle; this is a second, coarse guard.
	p.AllowAttrs("style").Matching(regexp.MustCompile(`^[^\\<>]*$`)).Globally()
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^obs-[a-z-]+$`)).Globally()
	p.AllowAttrs("align", "valign", "width", "height", "colspan", "rowspan").Matching(bluemonday.SpaceSeparatedTokens).
		OnElements("table", "tr", "td", "th", "col", "colgroup", "p", "div", "section", "img")
	p.AllowAttrs("color", "size", "face").OnElements("font")

	p.AllowElements("img")
	p.AllowRelativeURLs(true)
	p.AllowDataURIImages()
	p.AllowAttrs("src").Matching(regexp.MustCompile(
		`^(/a/[0-9a-f]{64}|data:image/(png|gif|jpeg|webp|svg\+xml)[;,][A-Za-z0-9+/=;,%._-]*)$`)).OnElements("img")
	p.AllowAttrs("alt", "title").OnElements("img")

	p.AllowElements("a")
	p.AllowAttrs("href").OnElements("a")
	p.AllowURLSchemes("http", "https")
	p.RequireNoReferrerOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	return p
}()

//go:embed page.html.tmpl
var pageTemplateText string

var pageTemplate = template.Must(template.New("page").Parse(pageTemplateText))

type pageData struct {
	Source
	Published, Captured string
	Body                template.HTML
	Missing             int
}

var cst = time.FixedZone("UTC+8", 8*3600)

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(cst).Format("2006-01-02 15:04")
}

// SingleFile is a reading page to be written out as one self-contained file,
// its /a/<sha256> image references replaced with data URIs.
type SingleFile struct {
	page  []byte
	refs  [][]int           // positions of the references in page
	types map[string]string // content type per sha256
	open  func(sha string) (io.ReadCloser, error)
}

// NewSingleFile prepares page for WriteTo. It looks up every image's
// content type first, so a missing image fails before anything is written.
func NewSingleFile(page []byte, contentType func(sha string) (string, error), open func(sha string) (io.ReadCloser, error)) (*SingleFile, error) {
	f := &SingleFile{page: page, refs: assetRef.FindAllIndex(page, -1), types: map[string]string{}, open: open}
	for _, r := range f.refs {
		sha := string(page[r[0]+3 : r[1]])
		if _, ok := f.types[sha]; ok {
			continue
		}
		ct, err := contentType(sha)
		if err != nil {
			return nil, fmt.Errorf("asset %s: %w", sha, err)
		}
		if ct == "" {
			ct = "application/octet-stream"
		}
		f.types[sha] = ct
	}
	return f, nil
}

// WriteTo writes the file, base64-encoding each image while it is read, so
// memory use does not grow with the images' size. An image used twice is
// read twice.
func (f *SingleFile) WriteTo(w io.Writer) (int64, error) {
	cw := &countingWriter{w: w}
	last := 0
	for _, r := range f.refs {
		sha := string(f.page[r[0]+3 : r[1]])
		if _, err := cw.Write(f.page[last:r[0]]); err != nil {
			return cw.n, err
		}
		if _, err := io.WriteString(cw, "data:"+f.types[sha]+";base64,"); err != nil {
			return cw.n, err
		}
		if err := f.writeImage(cw, sha); err != nil {
			return cw.n, err
		}
		last = r[1]
	}
	_, err := cw.Write(f.page[last:])
	return cw.n, err
}

func (f *SingleFile) writeImage(w io.Writer, sha string) error {
	rc, err := f.open(sha)
	if err != nil {
		return fmt.Errorf("asset %s: %w", sha, err)
	}
	defer rc.Close()
	enc := base64.NewEncoder(base64.StdEncoding, w)
	if _, err := io.Copy(enc, rc); err != nil {
		return fmt.Errorf("asset %s: %w", sha, err)
	}
	return enc.Close()
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

var assetRef = regexp.MustCompile(`/a/[0-9a-f]{64}`)
