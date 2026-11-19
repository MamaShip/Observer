// Package watchtest fakes WeChat for tests: pages by URL from fixtures,
// and a distinct small PNG for every image URL.
package watchtest

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/MamaShip/Observer/internal/wechat"
)

// Web is a fake WeChat.
type Web struct {
	mu     sync.Mutex
	pages  map[string][]byte
	assets int
}

// NewWeb returns an empty fake.
func NewWeb() *Web { return &Web{pages: map[string][]byte{}} }

// Set serves body at url (as WeChat would answer a GET).
func (w *Web) Set(url string, body []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pages[url] = body
}

// Assets is how many images were downloaded.
func (w *Web) Assets() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.assets
}

// Session returns a session on the fake; it satisfies watch.Session.
func (w *Web) Session() *Session { return &Session{w: w} }

// Session is one fake session.
type Session struct{ w *Web }

// FetchPage returns the page set for url, or a connection error.
func (s *Session) FetchPage(_ context.Context, url string, _ wechat.FetchMode) (*wechat.Page, error) {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	body, ok := s.w.pages[url]
	if !ok {
		return nil, errors.New("connection refused: " + url)
	}
	return &wechat.Page{URL: url, FinalURL: url, Status: 200, Body: body, WireBytes: int64(len(body))}, nil
}

// FetchAsset returns a PNG unique to url.
func (s *Session) FetchAsset(_ context.Context, url, _ string) (*wechat.Asset, error) {
	s.w.mu.Lock()
	s.w.assets++
	s.w.mu.Unlock()
	body := append([]byte("\x89PNG\r\n\x1a\n"), url...)
	return &wechat.Asset{URL: url, Status: 200, ContentType: "image/png", Size: int64(len(body)), Body: body, SHA256: sha256.Sum256(body)}, nil
}

// Close does nothing.
func (s *Session) Close() {}

// Fixture reads internal/wechat/testdata/name.
func Fixture(t testing.TB, name string) []byte {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "wechat", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The fixture article's short link.
const AliveURL = "https://mp.weixin.qq.com/s/q-WkvTApjcwqtgk9LY7Q-A"
