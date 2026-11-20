package snapserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MamaShip/Observer/internal/blob"
	"github.com/MamaShip/Observer/internal/store/db"
	"github.com/MamaShip/Observer/internal/store/storetest"
)

var key = []byte("0123456789abcdef0123456789abcdef")

func TestSigner(t *testing.T) {
	s, err := NewSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSigner([]byte("short")); err == nil {
		t.Error("short key accepted")
	}
	id, now := uuid.New(), time.Now()
	link, exp := s.URL("https://snap.example/", KindView, id, now)
	if !strings.HasPrefix(link, "https://snap.example/s/"+id.String()+"?") || exp.Sub(now) > ViewTTL {
		t.Fatalf("link = %s exp = %s", link, exp)
	}
	u, _ := url.Parse(link)
	q := u.Query()
	if !s.Verify(KindView, id, q.Get("exp"), q.Get("sig"), now) {
		t.Fatal("valid link rejected")
	}
	for name, ok := range map[string]bool{
		"wrong kind": s.Verify(KindDownload, id, q.Get("exp"), q.Get("sig"), now),
		"wrong id":   s.Verify(KindView, uuid.New(), q.Get("exp"), q.Get("sig"), now),
		"expired":    s.Verify(KindView, id, q.Get("exp"), q.Get("sig"), now.Add(ViewTTL+time.Second)),
		"longer exp": s.Verify(KindView, id, q.Get("exp")+"0", q.Get("sig"), now),
		"bad sig":    s.Verify(KindView, id, q.Get("exp"), strings.Repeat("0", 64), now),
		"no sig":     s.Verify(KindView, id, q.Get("exp"), "", now),
		"other key":  Signer{key: []byte("another key that is 32 bytes long")}.Verify(KindView, id, q.Get("exp"), q.Get("sig"), now),
	} {
		if ok {
			t.Errorf("%s: accepted", name)
		}
	}
	dl, _ := s.URL("https://snap.example", KindDownload, id, now)
	if !strings.Contains(dl, "/s/"+id.String()+"/single.html?") {
		t.Errorf("download link = %s", dl)
	}
}

func TestServer(t *testing.T) {
	ctx := context.Background()
	pool, _ := storetest.NewPool(t)
	q := db.New(pool)
	store, err := blob.NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	signer, _ := NewSigner(key)
	srv := New(q, store, signer, "https://observer.example", storetest.Logger())
	h := srv.Handler()

	png := []byte("\x89PNG\r\n\x1a\nimage")
	sha := sha256.Sum256(png)
	hexSHA := hex.EncodeToString(sha[:])
	if err := store.Put(ctx, blob.AssetKey(sha), bytes.NewReader(png), int64(len(png)), "image/png"); err != nil {
		t.Fatal(err)
	}
	if err := q.InsertAsset(ctx, db.InsertAssetParams{Sha256: sha[:], ContentType: "image/png", SizeBytes: int64(len(png))}); err != nil {
		t.Fatal(err)
	}
	var art uuid.UUID
	if err := pool.QueryRow(ctx, "INSERT INTO articles (status) VALUES ('alive') RETURNING id").Scan(&art); err != nil {
		t.Fatal(err)
	}
	snapID := uuid.New()
	page := `<html><body><img src="/a/` + hexSHA + `"></body></html>`
	if err := store.Put(ctx, blob.SnapshotKey(snapID.String(), "page.html"), strings.NewReader(page), int64(len(page)), "text/html"); err != nil {
		t.Fatal(err)
	}
	title := `标题/带:特殊*字符`
	if err := q.InsertSnapshot(ctx, db.InsertSnapshotParams{
		ID: snapID, ArticleID: art, CapturedAt: time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC), ContentHash: []byte{1}, Title: &title,
		HtmlKey: blob.SnapshotKey(snapID.String(), "page.html"), RawKey: "x", SizeBytes: int64(len(page)), AssetCount: 1,
	}); err != nil {
		t.Fatal(err)
	}

	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}
	path := func(link string) string { u, _ := url.Parse(link); return u.RequestURI() }

	view, _ := signer.URL("https://snap.example", KindView, snapID, time.Now())
	rec := get(path(view))
	if rec.Code != 200 || rec.Body.String() != page {
		t.Fatalf("page: %d %s", rec.Code, rec.Body)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"sandbox allow-same-origin ", "default-src 'none'", "img-src 'self' data:", "frame-ancestors https://observer.example"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "allow-scripts") || strings.Contains(csp, "script-src") {
		t.Errorf("CSP %q lets scripts run", csp)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Set-Cookie") != "" {
		t.Error("missing nosniff or unexpected cookie")
	}

	if rec := get("/s/" + snapID.String()); rec.Code != http.StatusForbidden {
		t.Errorf("unsigned page: %d", rec.Code)
	}
	if rec := get(strings.Replace(path(view), snapID.String(), uuid.NewString(), 1)); rec.Code != http.StatusForbidden {
		t.Errorf("signature for another snapshot: %d", rec.Code)
	}
	otherID := uuid.New()
	other, _ := signer.URL("https://snap.example", KindView, otherID, time.Now())
	if rec := get(path(other)); rec.Code != http.StatusNotFound {
		t.Errorf("signed link to a missing snapshot: %d", rec.Code)
	}

	dl, _ := signer.URL("https://snap.example", KindDownload, snapID, time.Now())
	rec = get(path(dl))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "data:image/png;base64,") || strings.Contains(rec.Body.String(), "/a/") {
		t.Fatalf("download: %d %s", rec.Code, rec.Body)
	}
	// While two downloads run, a third waits, then is turned away.
	downloadQueueing = 50 * time.Millisecond
	srv.downloads <- struct{}{}
	srv.downloads <- struct{}{}
	if busy := get(path(dl)); busy.Code != http.StatusServiceUnavailable || busy.Header().Get("Retry-After") == "" {
		t.Errorf("third download: %d %v", busy.Code, busy.Header())
	}
	<-srv.downloads
	if again := get(path(dl)); again.Code != 200 {
		t.Errorf("download once a slot is free: %d", again.Code)
	}
	<-srv.downloads
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") ||
		!strings.Contains(cd, "filename*=UTF-8''"+url.PathEscape("标题_带_特殊_字符-20261119.html")) {
		t.Errorf("Content-Disposition = %s", cd)
	}
	if rec := get(path(view) + "x"); rec.Code != http.StatusForbidden {
		t.Errorf("tampered link: %d", rec.Code)
	}
	if rec := get(strings.Replace(path(dl), "/single.html", "", 1)); rec.Code != http.StatusForbidden {
		t.Errorf("download signature used for viewing: %d", rec.Code)
	}

	rec = get("/a/" + hexSHA)
	body, _ := io.ReadAll(rec.Body)
	if rec.Code != 200 || !bytes.Equal(body, png) || rec.Header().Get("Content-Type") != "image/png" ||
		!strings.Contains(rec.Header().Get("Cache-Control"), "immutable") || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("asset: %d %v", rec.Code, rec.Header())
	}
	for _, bad := range []string{"/a/" + strings.ToUpper(hexSHA), "/a/" + strings.Repeat("0", 64), "/a/xyz"} {
		if rec := get(bad); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", bad, rec.Code)
		}
	}
	// ServeMux redirects dot segments to the cleaned path; nothing is served.
	if rec := get("/a/../etc/passwd"); rec.Code == http.StatusOK {
		t.Error("path traversal served content")
	}
}
