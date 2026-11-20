package wechat

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testSession(srv *httptest.Server) *Session {
	u, _ := url.Parse(srv.URL)
	return NewSession(Options{
		AllowPrivateNetworks: true,
		AllowHost:            func(h string) bool { return h == u.Hostname() },
	})
}

func gzipBytes(t *testing.T, b []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	return buf.Bytes()
}

func TestFetchPageGzipAndEarlyStop(t *testing.T) {
	// A large page whose content marker sits near the start.
	page := []byte(`<html><body><div id="js_content"><p>hello</p></div>` + strings.Repeat("<!-- padding -->", 200_000) + `</body></html>`)
	gz := gzipBytes(t, page)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "gzip" || r.Header.Get("User-Agent") == "" {
			t.Errorf("unexpected headers: %v", r.Header)
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(gz)
	}))
	defer srv.Close()
	s := testSession(srv)
	defer s.Close()

	full, err := s.FetchPage(context.Background(), srv.URL, FetchFull)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(full.Body, page) || full.Truncated {
		t.Fatalf("full body len=%d truncated=%v", len(full.Body), full.Truncated)
	}

	check, err := s.FetchPage(context.Background(), srv.URL, FetchCheck)
	if err != nil {
		t.Fatal(err)
	}
	if !check.Truncated || len(check.Body) >= len(page) || !bytes.Contains(check.Body, contentMarker) {
		t.Fatalf("check body len=%d truncated=%v", len(check.Body), check.Truncated)
	}
	if check.WireBytes <= 0 {
		t.Fatalf("wire bytes not counted")
	}
}

func TestFetchPageRejectsHosts(t *testing.T) {
	s := NewSession(Options{})
	_, err := s.FetchPage(context.Background(), "https://example.com/", FetchCheck)
	if !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("err = %v", err)
	}
	_, err = s.FetchAsset(context.Background(), "https://mp.weixin.qq.com.evil.com/a.png", "")
	if !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchBlocksPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	// Host allowed, but it resolves to loopback: the dialer must refuse.
	s := NewSession(Options{AllowHost: func(h string) bool { return h == u.Hostname() }})
	_, err := s.FetchPage(context.Background(), srv.URL, FetchCheck)
	if !errors.Is(err, ErrAddrNotAllowed) {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchRedirectToDisallowedHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/", http.StatusFound)
	}))
	defer srv.Close()
	_, err := testSession(srv).FetchPage(context.Background(), srv.URL, FetchCheck)
	if !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchAssetSendsReferer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Referer() != "https://mp.weixin.qq.com/s/x" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("png"))
	}))
	defer srv.Close()
	a, err := testSession(srv).FetchAsset(context.Background(), srv.URL+"/a.png", "https://mp.weixin.qq.com/s/x")
	if err != nil || a.Status != 200 || a.Size != 3 || a.ContentType != "image/png" {
		t.Fatalf("asset=%+v err=%v", a, err)
	}
}
