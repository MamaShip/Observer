package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandler(t *testing.T) {
	app := fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>app</title>")},
		"assets/index-abc.js":  {Data: []byte("console.log(1)")},
		"assets/index-abc.css": {Data: []byte("body{}")},
		"favicon.svg":          {Data: []byte("<svg/>")},
	}
	h := handler(app, CSP("https://snap.example"))
	cases := []struct {
		method, path string
		code         int
		body, ctype  string
		cache        string
		csp          bool
	}{
		{"GET", "/", 200, "<title>app</title>", "text/html", "no-cache", true},
		{"GET", "/watches/0193e4a1-0000-7000-8000-000000000000", 200, "<title>app</title>", "text/html", "no-cache", true},
		{"GET", "/login?next=%2Fsettings", 200, "<title>app</title>", "text/html", "no-cache", true},
		{"GET", "/index.html", 200, "<title>app</title>", "text/html", "no-cache", true},
		{"GET", "/assets/index-abc.js", 200, "console.log", "text/javascript", "immutable", false},
		{"GET", "/assets/index-abc.css", 200, "body{}", "text/css", "immutable", false},
		{"GET", "/favicon.svg", 200, "<svg/>", "image/svg+xml", "max-age=3600", false},
		{"GET", "/assets/index-old.js", 404, "", "", "", false},
		{"GET", "/robots.txt", 404, "", "", "", false},
		{"GET", "/../../etc/passwd", 200, "<title>app</title>", "text/html", "no-cache", true}, // the app, never a file outside the build
		{"HEAD", "/", 200, "", "text/html", "no-cache", true},
		{"POST", "/", 405, "", "", "", false},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		res := rec.Result()
		if res.StatusCode != c.code {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, res.StatusCode, c.code)
			continue
		}
		if c.code != 200 {
			continue
		}
		if !strings.Contains(rec.Body.String(), c.body) || (c.method == "HEAD" && rec.Body.Len() != 0) {
			t.Errorf("%s %s body = %q", c.method, c.path, rec.Body.String())
		}
		if !strings.HasPrefix(res.Header.Get("Content-Type"), c.ctype) || !strings.Contains(res.Header.Get("Cache-Control"), c.cache) {
			t.Errorf("%s %s headers: %v", c.method, c.path, res.Header)
		}
		if got := res.Header.Get("Content-Security-Policy"); (got != "") != c.csp {
			t.Errorf("%s %s CSP = %q", c.method, c.path, got)
		}
	}
}

func TestCSP(t *testing.T) {
	p := CSP("https://snap.example")
	for _, want := range []string{"script-src 'self'", "frame-src https://snap.example", "frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(p, want) {
			t.Errorf("CSP lacks %q: %s", want, p)
		}
	}
	if strings.Contains(p, "unsafe") {
		t.Errorf("CSP allows unsafe sources: %s", p)
	}
}

// TestEmbedded checks the real embedded build: the app when `make web` has
// run, the "not built" page otherwise.
func TestEmbedded(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler("").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/watches", nil))
	_, err := dist.Open("dist/app/index.html")
	switch built := err == nil; {
	case built && (rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `<div id="app">`)):
		t.Errorf("built app not served: %d %.200s", rec.Code, rec.Body.String())
	case !built && rec.Code != http.StatusServiceUnavailable:
		t.Errorf("no build, yet %d", rec.Code)
	}
}

func TestNotBuilt(t *testing.T) {
	rec := httptest.NewRecorder()
	handler(fstest.MapFS{}, "").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "make web") {
		t.Errorf("unbuilt app: %d %q", rec.Code, rec.Body.String())
	}
}
