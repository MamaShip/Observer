// Package snapserver serves snapshots on their own origin (snap.<domain>),
// which never sees login cookies: pages behind short-lived signed links,
// images by content hash, everything under a sandboxing CSP (plan.md §12).
package snapserver

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MamaShip/Observer/internal/blob"
	"github.com/MamaShip/Observer/internal/snapshot"
	"github.com/MamaShip/Observer/internal/store/db"
)

// Link kinds and lifetimes.
const (
	KindView     = "view"
	KindDownload = "download"
	ViewTTL      = time.Hour
	DownloadTTL  = 10 * time.Minute
)

// Signer makes and checks signed snapshot links.
type Signer struct{ key []byte }

// NewSigner uses key (at least 32 bytes) for HMAC-SHA256.
func NewSigner(key []byte) (Signer, error) {
	if len(key) < 32 {
		return Signer{}, errors.New("snapserver: signing key must be at least 32 bytes")
	}
	return Signer{key: key}, nil
}

func (s Signer) mac(kind string, id uuid.UUID, exp int64) string {
	m := hmac.New(sha256.New, s.key)
	fmt.Fprintf(m, "%s|%s|%d", kind, id, exp)
	return hex.EncodeToString(m.Sum(nil))
}

// URL returns a signed link to a snapshot on base (the snapshot origin).
func (s Signer) URL(base, kind string, id uuid.UUID, now time.Time) (string, time.Time) {
	ttl := ViewTTL
	path := "/s/" + id.String()
	if kind == KindDownload {
		ttl, path = DownloadTTL, path+"/single.html"
	}
	exp := now.Add(ttl).Truncate(time.Second)
	q := url.Values{"exp": {strconv.FormatInt(exp.Unix(), 10)}, "sig": {s.mac(kind, id, exp.Unix())}}
	return strings.TrimRight(base, "/") + path + "?" + q.Encode(), exp
}

// Verify checks a link's signature and expiry.
func (s Signer) Verify(kind string, id uuid.UUID, exp, sig string, now time.Time) bool {
	e, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || now.Unix() > e {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(s.mac(kind, id, e)))
}

// Server serves the snapshot origin.
type Server struct {
	q          *db.Queries
	blob       blob.Store
	signer     Signer
	mainOrigin string // allowed to embed snapshot pages
	log        *slog.Logger
	now        func() time.Time
	downloads  chan struct{} // single-file downloads in progress
}

// Single-file downloads are written as they are read, so each takes little
// memory, but every one reads all the article's images: only a few run at
// once, and others wait briefly before being turned away.
const maxDownloads = 2

var downloadQueueing = 15 * time.Second // a variable for tests

// New builds the server. mainOrigin is the main site's origin, e.g.
// https://observer.example, the only page allowed to frame snapshots.
func New(q *db.Queries, store blob.Store, signer Signer, mainOrigin string, log *slog.Logger) *Server {
	return &Server{q: q, blob: store, signer: signer, mainOrigin: mainOrigin, log: log.With("component", "snapserver"), now: time.Now,
		downloads: make(chan struct{}, maxDownloads)}
}

// Handler routes the snapshot origin.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /s/{id}", s.page)
	mux.HandleFunc("GET /s/{id}/single.html", s.download)
	mux.HandleFunc("GET /a/{sha}", s.asset)
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "User-agent: *\nDisallow: /\n")
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Robots-Tag", "noindex, nofollow")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		mux.ServeHTTP(w, r)
	})
}

// pageCSP sandboxes third-party content: no scripts, no requests outside
// this origin, framing only by the main site.
//
// allow-same-origin keeps the page on this origin. Without it the page gets
// an opaque origin, and its requests for /a/ images are cross-origin, which
// the images' Cross-Origin-Resource-Policy refuses. It is safe only because
// scripts stay disallowed: never add allow-scripts next to it.
func (s *Server) pageCSP() string {
	return "sandbox allow-same-origin allow-popups allow-popups-to-escape-sandbox; default-src 'none'; img-src 'self' data:; " +
		"style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors " + s.mainOrigin
}

// signedSnapshot validates the link and loads the snapshot, writing the
// error response itself when it returns false.
func (s *Server) signedSnapshot(w http.ResponseWriter, r *http.Request, kind string) (db.Snapshot, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	q := r.URL.Query()
	if err != nil || !s.signer.Verify(kind, id, q.Get("exp"), q.Get("sig"), s.now()) {
		http.Error(w, "链接无效或已过期，请回到观察详情页重新打开。", http.StatusForbidden)
		return db.Snapshot{}, false
	}
	snap, err := s.q.GetSnapshot(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return snap, false
	}
	if err != nil {
		s.fail(w, r, err)
		return snap, false
	}
	return snap, true
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.signedSnapshot(w, r, KindView)
	if !ok {
		return
	}
	body, err := s.read(r, snap.HtmlKey)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", s.pageCSP())
	h.Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(body)
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.signedSnapshot(w, r, KindDownload)
	if !ok {
		return
	}
	timer := time.NewTimer(downloadQueueing)
	defer timer.Stop()
	select {
	case s.downloads <- struct{}{}:
		defer func() { <-s.downloads }()
	case <-timer.C:
		w.Header().Set("Retry-After", "30")
		http.Error(w, "下载的人较多，请稍后再试。", http.StatusServiceUnavailable)
		return
	case <-r.Context().Done():
		return
	}
	page, err := s.read(r, snap.HtmlKey)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	file, err := SingleFile(r.Context(), s.q, s.blob, page)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", s.pageCSP())
	h.Set("Content-Disposition", attachment(deref(snap.Title), snap.CapturedAt))
	h.Set("Cache-Control", "private, no-store")
	bw := bufio.NewWriterSize(w, 64<<10)
	_, err = file.WriteTo(bw)
	if err == nil {
		err = bw.Flush()
	}
	if err != nil {
		// Part of the file is already sent: cut the connection so the
		// browser reports a failed download instead of saving half a page.
		s.log.Warn("single-file download interrupted", "snapshot", snap.ID, "err", err)
		panic(http.ErrAbortHandler)
	}
}

// SingleFile prepares a reading page as one self-contained file with its
// images from store (snapshot.SingleFile). The command-line export uses it too.
func SingleFile(ctx context.Context, q *db.Queries, store blob.Store, page []byte) (*snapshot.SingleFile, error) {
	hash := func(sha string) ([32]byte, error) {
		raw, err := hex.DecodeString(sha)
		if err != nil || len(raw) != 32 {
			return [32]byte{}, fmt.Errorf("bad hash %q", sha)
		}
		return [32]byte(raw), nil
	}
	return snapshot.NewSingleFile(page,
		func(sha string) (string, error) {
			h, err := hash(sha)
			if err != nil {
				return "", err
			}
			a, err := q.GetAsset(ctx, h[:])
			return a.ContentType, err
		},
		func(sha string) (io.ReadCloser, error) {
			h, err := hash(sha)
			if err != nil {
				return nil, err
			}
			return store.Get(ctx, blob.AssetKey(h))
		})
}

func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	raw, err := hex.DecodeString(r.PathValue("sha"))
	if err != nil || len(raw) != 32 || strings.ToLower(r.PathValue("sha")) != r.PathValue("sha") {
		http.NotFound(w, r)
		return
	}
	a, err := s.q.GetAsset(r.Context(), raw)
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rc, err := s.blob.Get(r.Context(), blob.AssetKey([32]byte(raw)))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rc.Close()
	h := w.Header()
	h.Set("Content-Type", a.ContentType)
	h.Set("Content-Length", strconv.FormatInt(a.SizeBytes, 10))
	// Content-addressed: the bytes behind this URL never change. The CSP
	// keeps an SVG opened directly from running scripts.
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = io.Copy(w, rc)
}

func (s *Server) read(r *http.Request, key string) ([]byte, error) {
	rc, err := s.blob.Get(r.Context(), key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("snapshot request failed", "path", r.URL.Path, "err", err)
	http.Error(w, "暂时无法读取备份，请稍后再试。", http.StatusBadGateway)
}

// attachment builds a Content-Disposition naming the file after the article.
func attachment(title string, captured time.Time) string {
	name := strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`\/:*?"<>|`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(title))
	if r := []rune(name); len(r) > 60 {
		name = string(r[:60])
	}
	if name == "" {
		name = "snapshot"
	}
	name += "-" + captured.UTC().Format("20060102") + ".html"
	return `attachment; filename="snapshot.html"; filename*=UTF-8''` + url.PathEscape(name)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
