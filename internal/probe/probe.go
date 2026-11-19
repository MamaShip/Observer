// Package probe implements `observer probe`, a diagnostic that fetches
// article links the way the service will and reports what it sees.
package probe

import (
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/MamaShip/Observer/internal/wechat"
)

// Config controls a probe run.
type Config struct {
	URLs        []string
	UserAgent   string        // preset name (ios, android, desktop) or a full UA string
	Repeat      int           // attempts per URL
	Interval    time.Duration // minimum gap between two page requests
	Full        bool          // also download the whole page
	Images      int           // download up to N images per article
	RefererTest bool          // also try each image without Referer
	FixtureDir  string        // save fetched pages here
	Window      int           // HTTP/2 receive window in bytes
	Head        bool          // periodic-style fetch reads only the page head (experimental)
	JSON        bool          // JSON lines instead of text
	Out         io.Writer
}

// Attempt is the report of one attempt on one URL.
type Attempt struct {
	Input     string        `json:"input"`
	Attempt   int           `json:"attempt"`
	Time      time.Time     `json:"time"`
	LinkKind  string        `json:"link_kind,omitempty"`
	LinkKey   string        `json:"link_key,omitempty"`
	FetchURL  string        `json:"fetch_url,omitempty"`
	LinkError string        `json:"link_error,omitempty"`
	Check     *FetchReport  `json:"check,omitempty"`
	Full      *FetchReport  `json:"full,omitempty"`
	Images    []ImageReport `json:"images,omitempty"`
	Fixture   string        `json:"fixture,omitempty"`
}

// FetchReport describes one page fetch and its analysis.
type FetchReport struct {
	Mode         string       `json:"mode"`
	Status       int          `json:"status,omitempty"`
	Proto        string       `json:"proto,omitempty"`
	FinalURL     string       `json:"final_url,omitempty"`
	WireBytes    int64        `json:"wire_bytes"`
	DecodedBytes int          `json:"decoded_bytes"`
	Truncated    bool         `json:"truncated,omitempty"`
	DurationMS   int64        `json:"duration_ms"`
	Error        string       `json:"error,omitempty"`
	Verdict      string       `json:"verdict,omitempty"`
	Reason       string       `json:"reason,omitempty"`
	Message      string       `json:"message,omitempty"`
	Detail       string       `json:"detail,omitempty"`
	Rule         string       `json:"rule,omitempty"`
	PageMID      string       `json:"page_mid,omitempty"`
	Meta         *wechat.Meta `json:"meta,omitempty"`

	body []byte
}

// ImageReport describes one image download.
type ImageReport struct {
	URL         string `json:"url"`
	WithReferer bool   `json:"with_referer"`
	Status      int    `json:"status,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size"`
	WireBytes   int64  `json:"wire_bytes"`
	DurationMS  int64  `json:"duration_ms"`
	SHA256      string `json:"sha256,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Run probes every URL and prints reports and a summary.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Out == nil {
		cfg.Out = os.Stdout
	}
	cfg.Repeat = max(cfg.Repeat, 1)
	ua := cfg.UserAgent
	if preset, ok := wechat.UserAgents[ua]; ok {
		ua = preset
	}
	if cfg.FixtureDir != "" {
		if err := os.MkdirAll(cfg.FixtureDir, 0o755); err != nil {
			return err
		}
	}

	p := &prober{cfg: cfg, ua: ua}
	var all []Attempt
	for _, in := range cfg.URLs {
		for i := 1; i <= cfg.Repeat; i++ {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			a := p.attempt(ctx, in, i)
			all = append(all, a)
			p.print(a)
		}
	}
	if !cfg.JSON {
		printSummary(cfg.Out, all)
	}
	return nil
}

type prober struct {
	cfg      Config
	ua       string
	lastPage time.Time
}

// pace keeps page requests at least cfg.Interval apart.
func (p *prober) pace(ctx context.Context) {
	if wait := time.Until(p.lastPage.Add(p.cfg.Interval)); wait > 0 && !p.lastPage.IsZero() {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
		}
	}
	p.lastPage = time.Now()
}

func (p *prober) attempt(ctx context.Context, in string, n int) Attempt {
	a := Attempt{Input: in, Attempt: n, Time: time.Now().UTC()}
	link, err := wechat.ParseLink(in)
	if err != nil {
		a.LinkError = err.Error()
		return a
	}
	a.LinkKind, a.LinkKey, a.FetchURL = string(link.Kind), link.Key, link.FetchURL

	s := wechat.NewSession(wechat.Options{UserAgent: p.ua, ReceiveWindow: p.cfg.Window})
	defer s.Close()

	p.pace(ctx)
	mode := wechat.FetchCheck
	if p.cfg.Head {
		mode = wechat.FetchHead
	}
	a.Check = fetch(ctx, s, link.FetchURL, mode)

	wantFull := p.cfg.Full || p.cfg.FixtureDir != "" ||
		(p.cfg.Images > 0 && a.Check.Verdict == string(wechat.VerdictAlive))
	if wantFull {
		p.pace(ctx)
		a.Full = fetch(ctx, s, link.FetchURL, wechat.FetchFull)
	}

	if p.cfg.Images > 0 {
		src := a.Full
		if src == nil || src.Meta == nil {
			src = a.Check
		}
		if src.Meta != nil {
			for i, img := range src.Meta.ImageURLs {
				if i >= p.cfg.Images {
					break
				}
				a.Images = append(a.Images, image(ctx, s, img, link.FetchURL))
				if p.cfg.RefererTest {
					a.Images = append(a.Images, image(ctx, s, img, ""))
				}
				time.Sleep(300 * time.Millisecond)
			}
		}
	}

	if p.cfg.FixtureDir != "" {
		src := a.Full
		if src == nil || len(src.body) == 0 {
			src = a.Check
		}
		if len(src.body) > 0 {
			path, err := saveFixture(p.cfg.FixtureDir, a, src)
			if err != nil {
				a.Fixture = "error: " + err.Error()
			} else {
				a.Fixture = path
			}
		}
	}
	return a
}

func fetch(ctx context.Context, s *wechat.Session, u string, mode wechat.FetchMode) *FetchReport {
	r := &FetchReport{Mode: map[wechat.FetchMode]string{wechat.FetchCheck: "check", wechat.FetchFull: "full", wechat.FetchHead: "head"}[mode]}
	page, err := s.FetchPage(ctx, u, mode)
	if page != nil {
		r.Status, r.Proto, r.FinalURL = page.Status, page.Proto, page.FinalURL
		r.WireBytes, r.DecodedBytes, r.Truncated = page.WireBytes, len(page.Body), page.Truncated
		r.DurationMS = page.Duration.Milliseconds()
		r.body = page.Body
	}
	if err != nil {
		r.Error = err.Error()
		r.Verdict = string(wechat.VerdictError)
		r.Rule = "fetch_error"
		return r
	}
	analyze := wechat.Analyze
	if mode == wechat.FetchHead {
		analyze = wechat.AnalyzeHead
	}
	res := analyze(page.Status, page.FinalURL, page.Body)
	r.Verdict, r.Reason, r.Message, r.Detail = string(res.Verdict), string(res.Reason), res.Message, res.Detail
	r.Rule, r.PageMID, r.Meta = res.Rule, res.PageMID, res.Meta
	return r
}

func image(ctx context.Context, s *wechat.Session, u, referer string) ImageReport {
	r := ImageReport{URL: u, WithReferer: referer != ""}
	a, err := s.FetchAsset(ctx, u, referer)
	if a != nil {
		r.Status, r.ContentType, r.Size = a.Status, a.ContentType, a.Size
		r.WireBytes, r.DurationMS = a.WireBytes, a.Duration.Milliseconds()
		r.SHA256 = hex.EncodeToString(a.SHA256[:])
	}
	if err != nil {
		r.Error = err.Error()
	}
	return r
}

// Values that may identify a reader session are redacted from fixtures.
var reSecret = regexp.MustCompile(`((?:pass_ticket|appmsg_token|wxtoken|wxuin|devicetype|exportkey)["']?\s*[:=]\s*["']?)[^"'&\s<>]{4,}`)

func saveFixture(dir string, a Attempt, r *FetchReport) (string, error) {
	label := r.Verdict
	if r.Reason != "" {
		label += "-" + r.Reason
	}
	name := fmt.Sprintf("%s-%s-%s", label, a.Time.Format("20060102T150405Z"), shortID(a.LinkKey))
	base := filepath.Join(dir, name)

	f, err := os.Create(base + ".html.gz")
	if err != nil {
		return "", err
	}
	zw := gzip.NewWriter(f)
	_, werr := zw.Write(reSecret.ReplaceAll(r.body, []byte("${1}REDACTED")))
	cerr := zw.Close()
	ferr := f.Close()
	if err := firstErr(werr, cerr, ferr); err != nil {
		return "", err
	}

	meta := a
	meta.Images = nil
	meta.Fixture = ""
	jb, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(base+".json", jb, 0o644); err != nil {
		return "", err
	}
	return base + ".html.gz", nil
}

func shortID(s string) string {
	s = strings.TrimPrefix(s, "https://mp.weixin.qq.com/s")
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return -1
	}, s)
	if len(s) > 24 {
		s = s[len(s)-24:]
	}
	return s
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func (p *prober) print(a Attempt) {
	w := p.cfg.Out
	if p.cfg.JSON {
		b, _ := json.Marshal(a)
		fmt.Fprintln(w, string(b))
		return
	}
	fmt.Fprintf(w, "\n[%s #%d] %s\n", a.Time.Format(time.RFC3339), a.Attempt, a.Input)
	if a.LinkError != "" {
		fmt.Fprintf(w, "  link:    REJECTED %s\n", a.LinkError)
		return
	}
	fmt.Fprintf(w, "  link:    %s  key=%s\n", a.LinkKind, a.LinkKey)
	if a.LinkKind == string(wechat.LinkLong) && !strings.Contains(a.FetchURL, "chksm=") {
		fmt.Fprintf(w, "  warning: long link without chksm; WeChat usually answers with the verification page\n")
	}
	for _, r := range []*FetchReport{a.Check, a.Full} {
		if r == nil {
			continue
		}
		fmt.Fprintf(w, "  %-6s   %s", r.Mode+":", strings.ToUpper(r.Verdict))
		if r.Reason != "" {
			fmt.Fprintf(w, " (%s)", r.Reason)
		}
		fmt.Fprintf(w, "  rule=%s  http=%d %s  wire=%s decoded=%s%s  %dms\n",
			r.Rule, r.Status, r.Proto, size(r.WireBytes), size(int64(r.DecodedBytes)),
			map[bool]string{true: " (stopped early)"}[r.Truncated], r.DurationMS)
		if r.Error != "" {
			fmt.Fprintf(w, "           error: %s\n", r.Error)
		}
		if r.Message != "" {
			fmt.Fprintf(w, "           message: %s\n", r.Message)
		}
		if r.Detail != "" {
			fmt.Fprintf(w, "           detail:  %s\n", truncate(r.Detail, 160))
		}
		if r.FinalURL != "" && r.FinalURL != a.FetchURL {
			fmt.Fprintf(w, "           final url: %s\n", r.FinalURL)
		}
		if r.PageMID != "" {
			fmt.Fprintf(w, "           page_mid: %s\n", r.PageMID)
		}
	}
	if m := pickMeta(a); m != nil {
		fmt.Fprintf(w, "  meta:    key=%s sn=%s show_type=%s\n", m.CanonicalKey(), m.Sn, m.ItemShowType)
		fmt.Fprintf(w, "           title=%s\n", truncate(m.Title, 80))
		fmt.Fprintf(w, "           account=%s (%s) author=%s\n", m.AccountName, m.AccountID, m.Author)
		if !m.PublishedAt.IsZero() {
			fmt.Fprintf(w, "           published=%s\n", m.PublishedAt.Format(time.RFC3339))
		}
		if m.MsgLink != "" {
			fmt.Fprintf(w, "           msg_link=%s\n", m.MsgLink)
		}
		fmt.Fprintf(w, "           content=%d chars, %d images\n", m.ContentRunes, len(m.ImageURLs))
	}
	for _, im := range a.Images {
		ref := "referer"
		if !im.WithReferer {
			ref = "no-referer"
		}
		fmt.Fprintf(w, "  image:   [%s] http=%d %s %s %dms %s", ref, im.Status, im.ContentType, size(im.Size), im.DurationMS, truncate(im.URL, 90))
		if im.Error != "" {
			fmt.Fprintf(w, " error=%s", im.Error)
		}
		fmt.Fprintln(w)
	}
	if a.Fixture != "" {
		fmt.Fprintf(w, "  fixture: %s\n", a.Fixture)
	}
}

func pickMeta(a Attempt) *wechat.Meta {
	if a.Full != nil && a.Full.Meta != nil {
		return a.Full.Meta
	}
	if a.Check != nil {
		return a.Check.Meta
	}
	return nil
}

func printSummary(w io.Writer, all []Attempt) {
	type agg struct {
		verdicts     map[string]int
		firstBlocked int
		checkWire    []int64
		fullWire     []int64
	}
	order := []string{}
	byInput := map[string]*agg{}
	for _, a := range all {
		g := byInput[a.Input]
		if g == nil {
			g = &agg{verdicts: map[string]int{}}
			byInput[a.Input] = g
			order = append(order, a.Input)
		}
		if a.LinkError != "" {
			g.verdicts["rejected"]++
			continue
		}
		if a.Check != nil {
			g.verdicts[a.Check.Verdict]++
			g.checkWire = append(g.checkWire, a.Check.WireBytes)
			if a.Check.Verdict == string(wechat.VerdictBlocked) && g.firstBlocked == 0 {
				g.firstBlocked = a.Attempt
			}
		}
		if a.Full != nil {
			g.fullWire = append(g.fullWire, a.Full.WireBytes)
		}
	}
	fmt.Fprintf(w, "\n==== summary (%d attempts) ====\n", len(all))
	for _, in := range order {
		g := byInput[in]
		var parts []string
		for _, v := range []string{"alive", "deleted", "invalid", "blocked", "error", "rejected"} {
			if g.verdicts[v] > 0 {
				parts = append(parts, fmt.Sprintf("%s=%d", v, g.verdicts[v]))
			}
		}
		fmt.Fprintf(w, "%s\n  %s", in, strings.Join(parts, " "))
		if g.firstBlocked > 0 {
			fmt.Fprintf(w, "  first_blocked_at=#%d", g.firstBlocked)
		}
		if len(g.checkWire) > 0 {
			fmt.Fprintf(w, "  avg_check_wire=%s", size(avg(g.checkWire)))
		}
		if len(g.fullWire) > 0 {
			fmt.Fprintf(w, "  avg_full_wire=%s", size(avg(g.fullWire)))
		}
		fmt.Fprintln(w)
	}
}

func avg(xs []int64) int64 {
	var s int64
	for _, x := range xs {
		s += x
	}
	return s / int64(len(xs))
}

func size(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.2fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
