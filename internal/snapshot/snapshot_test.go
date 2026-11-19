package snapshot

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fakeStore "stores" every image by hashing its URL, failing for URLs that
// contain any of fail.
func fakeStore(fail ...string) (AssetFunc, *[]string) {
	var calls []string
	return func(_ context.Context, u string) (AssetRef, error) {
		calls = append(calls, u)
		for _, f := range fail {
			if strings.Contains(u, f) {
				return AssetRef{}, errors.New("download failed")
			}
		}
		return AssetRef{SHA256: sha256.Sum256([]byte(u)), ContentType: "image/png"}, nil
	}, &calls
}

var src = Source{
	URL:         "https://mp.weixin.qq.com/s/q-WkvTApjcwqtgk9LY7Q-A",
	Title:       "标题 <b>&",
	AccountName: "健康成都官微",
	PublishedAt: time.Date(2020, 5, 27, 0, 57, 19, 0, time.UTC),
	CapturedAt:  time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC),
}

func TestBuildFixture(t *testing.T) {
	raw, err := os.ReadFile("../wechat/testdata/alive_article.html")
	if err != nil {
		t.Fatal(err)
	}
	store, calls := fakeStore()
	res, err := Build(context.Background(), raw, src, store)
	if err != nil {
		t.Fatal(err)
	}
	page := string(res.HTML)

	// 7 <img> plus 2 background images, one of which is also an <img>:
	// 8 distinct images, each downloaded once.
	if len(*calls) != 8 || len(res.Assets) != 8 || res.Missing != 0 {
		t.Fatalf("calls=%d assets=%d missing=%d: %v", len(*calls), len(res.Assets), res.Missing, *calls)
	}
	for _, c := range *calls {
		if !strings.HasPrefix(c, "https://mmbiz.qpic.cn/") {
			t.Errorf("unexpected asset URL %s", c)
		}
	}
	if n := strings.Count(page, `src="/a/`); n != 7 {
		t.Errorf("%d img src rewritten, want 7", n)
	}
	if n := strings.Count(page, `url(&#39;/a/`); n != 2 {
		t.Errorf("%d background images rewritten, want 2", n)
	}
	for _, bad := range []string{"mmbiz.qpic.cn", "<script", "visibility: hidden", "opacity: 0;", "data-src"} {
		if strings.Contains(page, bad) {
			t.Errorf("page still contains %q", bad)
		}
	}
	for _, want := range []string{
		"<title>标题 &lt;b&gt;&amp;</title>", "健康成都官微", "2020-05-27 08:57", "2026-11-19 20:00",
		"截至5月26日24时", `href="https://mp.weixin.qq.com/s/q-WkvTApjcwqtgk9LY7Q-A"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page is missing %q", want)
		}
	}
	if res.TextRunes < 300 || res.ContentHash == [32]byte{} {
		t.Errorf("text runes = %d", res.TextRunes)
	}

	// Same input, same content hash; a failed image is counted and marked.
	store2, _ := fakeStore("wx_fmt=gif")
	res2, err := Build(context.Background(), raw, src, store2)
	if err != nil {
		t.Fatal(err)
	}
	if res2.ContentHash != res.ContentHash {
		t.Error("content hash depends on image storage")
	}
	if res2.Missing != 2 || !strings.Contains(string(res2.HTML), `class="obs-missing"`) ||
		!strings.Contains(string(res2.HTML), "有 2 张图片未能备份") {
		t.Errorf("missing=%d, page does not mark missing images", res2.Missing)
	}
}

func TestBuildNoContent(t *testing.T) {
	raw, err := os.ReadFile("../wechat/testdata/deleted_by_author.html")
	if err != nil {
		t.Fatal(err)
	}
	store, _ := fakeStore()
	if _, err := Build(context.Background(), raw, src, store); !errors.Is(err, ErrNoContent) {
		t.Fatalf("err = %v, want ErrNoContent", err)
	}
}

func TestBuildHostile(t *testing.T) {
	raw := []byte(`<html><body><div id="js_content">
<p onclick="alert(1)">点击</p>
<img src="x" onerror="alert(2)">
<img data-src="https://evil.example/track.gif">
<img data-src="//mmbiz.qpic.cn/mmbiz_jpg/abc/640?wx_fmt=jpeg">
<a href="javascript:alert(3)">js link</a>
<a href="https://example.com/page">ok link</a>
<p style="background: url('https://evil.example/x.png'); color: red">外链背景</p>
<p style="color: blue; behavior: url(x.htc); width: expression(alert(4))">样式注入</p>
<p style="position: fixed; top: 0">fixed</p>
<p style="background-image: url(&quot;data:image/svg+xml;charset=utf-8,%3Csvg%3E&quot;); margin: 0">svg</p>
<iframe class="video_iframe" data-src="https://v.qq.com/x"></iframe>
<mpvoice name="voice"></mpvoice>
<script>alert(5)</script>
<svg onload="alert(6)"><circle/></svg>
<form action="https://evil.example"><input name="q"></form>
<span class="rich_media_content js_underline">class kept?</span>
</div></body></html>`)
	store, calls := fakeStore()
	res, err := Build(context.Background(), raw, src, store)
	if err != nil {
		t.Fatal(err)
	}
	body := string(res.HTML)
	body = body[strings.Index(body, `<div class="obs-content">`):]

	if len(*calls) != 1 || (*calls)[0] != "https://mmbiz.qpic.cn/mmbiz_jpg/abc/640?wx_fmt=jpeg" {
		t.Errorf("downloads = %v, want only the CDN image (as https)", *calls)
	}
	for _, bad := range []string{"onclick", "onerror", "onload", "javascript:", "evil.example", "expression",
		"behavior", "position: fixed", "<script", "<iframe", "<svg", "<form", "<input", "mpvoice", "rich_media_content"} {
		if strings.Contains(body, bad) {
			t.Errorf("sanitized body contains %q", bad)
		}
	}
	for _, want := range []string{"color: red", "color: blue", "[视频]", "[音频]", "ok link", `rel="noreferrer`,
		"url(&#39;data:image/svg+xml;charset=utf-8,%3Csvg%3E&#39;)", "class kept?"} {
		if !strings.Contains(body, want) {
			t.Errorf("sanitized body is missing %q", want)
		}
	}
	if n := strings.Count(body, `class="obs-missing"`); n != 2 {
		t.Errorf("%d images marked missing, want 2 (bad src and non-CDN)", n)
	}
}

func TestCleanStyle(t *testing.T) {
	lookup := func(u string) (string, bool) {
		if u == "https://mmbiz.qpic.cn/a.png" {
			return "/a/" + strings.Repeat("0", 64), true
		}
		return "", false
	}
	cases := map[string]string{
		"color: red; visibility: hidden; opacity: 0 !important": "color: red",
		"opacity: 0.5": "opacity: 0.5",
		`background-image: url("https://mmbiz.qpic.cn/a.png")`:              "background-image: url('/a/" + strings.Repeat("0", 64) + "')",
		"background: url(https://other.example/a.png) no-repeat; margin: 0": "margin: 0",
		"font-family: 'a;b', serif; color: red":                             "font-family: 'a;b', serif; color: red",
		"width: calc(100% - 2px); ; :x; y:":                                 "width: calc(100% - 2px)",
		`color: red\;`:                                                      "",
		"position: absolute; POSITION: Fixed":                               "position: absolute",
	}
	for in, want := range cases {
		if got := cleanStyle(in, lookup); got != want {
			t.Errorf("cleanStyle(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestInline(t *testing.T) {
	sha := sha256.Sum256([]byte("img"))
	h := hex.EncodeToString(sha[:])
	page := []byte(`<img src="/a/` + h + `"><p style="background: url('/a/` + h + `')">`)
	calls := 0
	out, err := Inline(page, func(s string) ([]byte, string, error) {
		calls++
		if s != h {
			t.Fatalf("asked for %s", s)
		}
		return []byte("PNG"), "image/png", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("fetched %d times, want 1 (cached)", calls)
	}
	if n := regexp.MustCompile(`data:image/png;base64,UE5H`).FindAllIndex(out, -1); len(n) != 2 {
		t.Fatalf("inlined %d references: %s", len(n), out)
	}
	_, err = Inline(page, func(string) ([]byte, string, error) { return nil, "", errors.New("gone") })
	if err == nil {
		t.Fatal("missing asset not reported")
	}
}

// TestBuildFile builds a snapshot from a saved page, for eyeballing:
//
//	SNAPSHOT_RAW=page.html.gz SNAPSHOT_OUT=out.html go test -run TestBuildFile ./internal/snapshot
func TestBuildFile(t *testing.T) {
	path := os.Getenv("SNAPSHOT_RAW")
	if path == "" {
		t.Skip("SNAPSHOT_RAW not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(path, ".gz") {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if raw, err = io.ReadAll(zr); err != nil {
			t.Fatal(err)
		}
	}
	store, calls := fakeStore()
	start := time.Now()
	res, err := Build(context.Background(), raw, src, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("input %d KB -> page %d KB, %d images, %d text runes, %s",
		len(raw)>>10, len(res.HTML)>>10, len(*calls), res.TextRunes, time.Since(start))
	if out := os.Getenv("SNAPSHOT_OUT"); out != "" {
		if err := os.WriteFile(out, res.HTML, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
