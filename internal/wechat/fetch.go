package wechat

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// UserAgents holds the presets the fetcher can impersonate.
var UserAgents = map[string]string{
	"ios":     "Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Mobile/15E148 Safari/604.1",
	"android": "Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Mobile Safari/537.36",
	"desktop": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36",
}

// FetchMode controls how much of an article page is downloaded.
type FetchMode int

const (
	// FetchCheck stops reading once the page can be classified, which is
	// enough for periodic checks. Article pages are ~4 MB decoded, but the
	// content node starts around 0.6 MB.
	FetchCheck FetchMode = iota
	// FetchFull downloads the whole page, for snapshots.
	FetchFull
	// FetchHead stops right after the Open Graph tags in the page head
	// (~20 KB decoded). Experimental: see AnalyzeHead.
	FetchHead
)

const (
	checkTailBytes = 64 << 10 // read this far past the content marker in FetchCheck
	checkMaxBytes  = 2 << 20  // hard cap for FetchCheck
	headTailBytes  = 1 << 10  // read this far past og:type in FetchHead
	headMaxBytes   = 256 << 10
	fullMaxBytes   = 16 << 20 // hard cap for FetchFull
	assetMaxBytes  = 20 << 20
	maxRedirects   = 5
)

var (
	contentMarker = []byte(`id="js_content"`)
	headMarker    = []byte(`property="og:type"`)

	ErrHostNotAllowed = errors.New("wechat: host not allowed")
	ErrAddrNotAllowed = errors.New("wechat: address not allowed")
	ErrTooLarge       = errors.New("wechat: response too large")
)

// Options configures a Session.
type Options struct {
	UserAgent string        // defaults to the "ios" preset
	Timeout   time.Duration // per request, defaults to 20s
	// ReceiveWindow caps HTTP/2 receive buffers so that stopping early in
	// FetchCheck/FetchHead actually saves traffic. Defaults to 16 KiB, which
	// measured 26 KB on the wire for a head fetch versus 58 KB at 128 KiB.
	ReceiveWindow int
	// AllowHost overrides the host allowlist (tests only).
	AllowHost func(host string) bool
	// AllowPrivateNetworks disables the public-address check (tests only).
	AllowPrivateNetworks bool
}

// Session is a cookie-holding client with one fixed User-Agent. Use one
// session per job; it is safe for sequential use only.
type Session struct {
	client   *http.Client
	ua       string
	timeout  time.Duration
	override func(string) bool // replaces host checks in tests
	wire     atomic.Int64
}

// NewSession builds a Session.
func NewSession(opts Options) *Session {
	s := &Session{ua: opts.UserAgent, timeout: opts.Timeout, override: opts.AllowHost}
	if s.ua == "" {
		s.ua = UserAgents["ios"]
	}
	if s.timeout == 0 {
		s.timeout = 20 * time.Second
	}
	window := opts.ReceiveWindow
	if window == 0 {
		window = 16 << 10
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if !opts.AllowPrivateNetworks {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || !isPublicIP(ip) {
				return fmt.Errorf("%w: %s", ErrAddrNotAllowed, host)
			}
			return nil
		}
	}

	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return &countingConn{Conn: c, n: &s.wire}, nil
		},
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		IdleConnTimeout:       60 * time.Second,
		MaxIdleConnsPerHost:   2,
		DisableCompression:    true, // gzip is decoded here, so wire bytes stay measurable
		HTTP2: &http.HTTP2Config{
			MaxReceiveBufferPerStream:     window,
			MaxReceiveBufferPerConnection: window * 2,
		},
	}
	jar, _ := cookiejar.New(nil)
	s.client = &http.Client{
		Transport: tr,
		Jar:       jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("wechat: too many redirects")
			}
			if h := req.URL.Hostname(); !s.allowed(h, IsPageHost(h) || IsAssetHost(h)) {
				return fmt.Errorf("%w: redirect to %s", ErrHostNotAllowed, req.URL.Hostname())
			}
			return nil
		},
	}
	return s
}

// Close releases idle connections.
func (s *Session) Close() { s.client.CloseIdleConnections() }

// UserAgent returns the session's User-Agent.
func (s *Session) UserAgent() string { return s.ua }

// Page is a fetched article page.
type Page struct {
	URL        string
	FinalURL   string
	Status     int
	Proto      string
	Body       []byte // decoded; may be partial, see Truncated
	Truncated  bool   // reading stopped early (FetchCheck) or hit a cap
	WireBytes  int64  // bytes read from the network, including TLS overhead
	Duration   time.Duration
	RemoteAddr string
}

// FetchPage downloads an article page.
func (s *Session) FetchPage(ctx context.Context, rawURL string, mode FetchMode) (*Page, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if !s.allowed(u.Hostname(), IsPageHost(u.Hostname())) {
		return nil, fmt.Errorf("%w: %s", ErrHostNotAllowed, u.Hostname())
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip")

	start, wire0 := time.Now(), s.wire.Load()
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	page := &Page{URL: rawURL, FinalURL: resp.Request.URL.String(), Status: resp.StatusCode, Proto: resp.Proto}

	body, truncated, readErr := readPage(resp, mode)
	resp.Body.Close()
	page.Body, page.Truncated = body, truncated
	page.Duration = time.Since(start)
	page.WireBytes = s.wire.Load() - wire0
	if readErr != nil {
		return page, readErr
	}
	return page, nil
}

func (s *Session) allowed(host string, byDefault bool) bool {
	if s.override != nil {
		return s.override(host)
	}
	return byDefault
}

func readPage(resp *http.Response, mode FetchMode) ([]byte, bool, error) {
	var r io.Reader = resp.Body
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, false, err
		}
		defer gz.Close()
		r = gz
	}

	limit, marker, tail := fullMaxBytes, []byte(nil), 0
	switch mode {
	case FetchCheck:
		limit, marker, tail = checkMaxBytes, contentMarker, checkTailBytes
	case FetchHead:
		limit, marker, tail = headMaxBytes, headMarker, headTailBytes
	}
	var buf bytes.Buffer
	chunk := make([]byte, 16<<10)
	markerAt, searchFrom := -1, 0
	for {
		n, err := r.Read(chunk)
		buf.Write(chunk[:n])
		if marker != nil {
			if markerAt < 0 {
				if i := bytes.Index(buf.Bytes()[searchFrom:], marker); i >= 0 {
					markerAt = searchFrom + i
				} else if l := buf.Len() - len(marker); l > searchFrom {
					searchFrom = l
				}
			}
			if markerAt >= 0 && buf.Len() >= markerAt+tail {
				return buf.Bytes(), true, nil
			}
		}
		if buf.Len() >= limit {
			if mode != FetchFull {
				return buf.Bytes(), true, nil
			}
			return buf.Bytes(), true, ErrTooLarge
		}
		if err == io.EOF {
			return buf.Bytes(), false, nil
		}
		if err != nil {
			return buf.Bytes(), false, err
		}
	}
}

// Asset is a downloaded image or other resource.
type Asset struct {
	URL         string
	Status      int
	ContentType string
	Size        int64
	SHA256      [32]byte
	Body        []byte
	WireBytes   int64
	Duration    time.Duration
}

// FetchAsset downloads a resource referenced by an article. Pass the
// article URL as referer; WeChat's image CDN checks it.
func (s *Session) FetchAsset(ctx context.Context, rawURL, referer string) (*Asset, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if !s.allowed(u.Hostname(), IsAssetHost(u.Hostname())) {
		return nil, fmt.Errorf("%w: %s", ErrHostNotAllowed, u.Hostname())
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.ua)
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	start, wire0 := time.Now(), s.wire.Load()
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, assetMaxBytes+1))
	a := &Asset{
		URL:         rawURL,
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Size:        int64(len(body)),
		SHA256:      sha256.Sum256(body),
		Body:        body,
		Duration:    time.Since(start),
		WireBytes:   s.wire.Load() - wire0,
	}
	if err != nil {
		return a, err
	}
	if a.Size > assetMaxBytes {
		return a, ErrTooLarge
	}
	return a, nil
}

// IsPageHost reports whether host serves article pages.
func IsPageHost(host string) bool { return strings.EqualFold(host, ArticleHost) }

// IsAssetHost reports whether host may serve article images and media.
func IsAssetHost(host string) bool {
	h := strings.ToLower(host)
	return strings.HasSuffix(h, ".qpic.cn") || strings.HasSuffix(h, ".qlogo.cn") || h == "res.wx.qq.com"
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func isPublicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || cgnat.Contains(ip))
}

type countingConn struct {
	net.Conn
	n *atomic.Int64
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.n.Add(int64(n))
	return n, err
}
