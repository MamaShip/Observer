// Package netcheck implements `observer netcheck`: latency to the services
// Observer depends on, SMTP port reachability, and optional S3 throughput.
package netcheck

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// WasabiRegions lists region ids to try; unknown ones simply fail DNS.
var WasabiRegions = []string{
	"ap-east-1", "ap-northeast-1", "ap-northeast-2", "ap-southeast-1", "ap-southeast-2",
	"us-west-1", "us-east-1", "us-east-2", "us-central-1", "ca-central-1",
	"eu-central-1", "eu-central-2", "eu-west-1", "eu-west-2", "eu-west-3", "eu-south-1",
}

// DefaultWasabiRegions is the subset tested unless told otherwise.
var DefaultWasabiRegions = []string{"ap-east-1", "ap-northeast-1", "ap-northeast-2", "ap-southeast-1", "ap-southeast-2", "us-west-1"}

// Target is one endpoint to measure.
type Target struct {
	Name string
	Kind string // "https", "smtp" (plain, reads banner) or "smtps" (implicit TLS)
	Addr string // URL for https, host:port otherwise
}

// Targets builds the default target list.
func Targets(wasabiRegions []string, smtpHost string) []Target {
	ts := []Target{
		{"wechat article", "https", "https://mp.weixin.qq.com/"},
		{"wechat images", "https", "https://mmbiz.qpic.cn/"},
		{"wechat static", "https", "https://res.wx.qq.com/"},
	}
	for _, r := range wasabiRegions {
		ts = append(ts, Target{"wasabi " + r, "https", "https://s3." + r + ".wasabisys.com/"})
	}
	if smtpHost != "" {
		ts = append(ts,
			Target{"smtp 25", "smtp", net.JoinHostPort(smtpHost, "25")},
			Target{"smtp 587", "smtp", net.JoinHostPort(smtpHost, "587")},
			Target{"smtps 465", "smtps", net.JoinHostPort(smtpHost, "465")},
		)
	}
	return ts
}

type sample struct {
	dns, connect, tls, ttfb, total time.Duration
	status                         int
	banner                         string
	err                            error
}

// Run measures every target `samples` times and prints medians.
func Run(ctx context.Context, w io.Writer, targets []Target, samples int) {
	samples = max(samples, 1)
	fmt.Fprintf(w, "%-22s %-6s %8s %8s %8s %8s %8s  %s\n", "target", "kind", "dns", "connect", "tls", "ttfb", "total", "result")
	for _, t := range targets {
		var ok []sample
		var lastErr error
		var last sample
		for i := 0; i < samples; i++ {
			var s sample
			if t.Kind == "https" {
				s = measureHTTPS(ctx, t.Addr)
			} else {
				s = measureSMTP(ctx, t.Addr, t.Kind == "smtps")
			}
			if s.err != nil {
				lastErr = s.err
			} else {
				ok = append(ok, s)
				last = s
			}
		}
		if len(ok) == 0 {
			fmt.Fprintf(w, "%-22s %-6s %8s %8s %8s %8s %8s  FAIL %v\n", t.Name, t.Kind, "-", "-", "-", "-", "-", shortErr(lastErr))
			continue
		}
		med := func(f func(sample) time.Duration) string {
			ds := make([]time.Duration, len(ok))
			for i, s := range ok {
				ds[i] = f(s)
			}
			slices.Sort(ds)
			d := ds[len(ds)/2]
			if d == 0 {
				return "-"
			}
			return fmt.Sprintf("%dms", d.Milliseconds())
		}
		result := fmt.Sprintf("ok %d/%d", len(ok), samples)
		if last.status != 0 {
			result += fmt.Sprintf(" http=%d", last.status)
		}
		if last.banner != "" {
			result += " banner=" + last.banner
		}
		fmt.Fprintf(w, "%-22s %-6s %8s %8s %8s %8s %8s  %s\n", t.Name, t.Kind,
			med(func(s sample) time.Duration { return s.dns }), med(func(s sample) time.Duration { return s.connect }),
			med(func(s sample) time.Duration { return s.tls }), med(func(s sample) time.Duration { return s.ttfb }),
			med(func(s sample) time.Duration { return s.total }), result)
	}
}

func measureHTTPS(ctx context.Context, u string) sample {
	var s sample
	var dnsStart, connStart, tlsStart, start time.Time
	trace := &httptrace.ClientTrace{
		DNSStart:             func(httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone:              func(httptrace.DNSDoneInfo) { s.dns = time.Since(dnsStart) },
		ConnectStart:         func(string, string) { connStart = time.Now() },
		ConnectDone:          func(string, string, error) { s.connect = time.Since(connStart) },
		TLSHandshakeStart:    func() { tlsStart = time.Now() },
		TLSHandshakeDone:     func(tls.ConnectionState, error) { s.tls = time.Since(tlsStart) },
		GotFirstResponseByte: func() { s.ttfb = time.Since(start) },
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// GET, not HEAD: mp.weixin.qq.com answers HEAD with a body, which Go logs as a protocol error.
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, u, nil)
	if err != nil {
		s.err = err
		return s
	}
	client := &http.Client{
		Transport:     &http.Transport{DisableKeepAlives: true, ForceAttemptHTTP2: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	start = time.Now()
	resp, err := client.Do(req)
	s.total = time.Since(start)
	if err != nil {
		s.err = err
		return s
	}
	resp.Body.Close()
	s.status = resp.StatusCode
	return s
}

func measureSMTP(ctx context.Context, addr string, implicitTLS bool) sample {
	var s sample
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	start := time.Now()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	s.connect = time.Since(start)
	if err != nil {
		s.err = err
		return s
	}
	defer conn.Close()
	if implicitTLS {
		host, _, _ := net.SplitHostPort(addr)
		tc := tls.Client(conn, &tls.Config{ServerName: host})
		t0 := time.Now()
		if err := tc.HandshakeContext(ctx); err != nil {
			s.err = err
			return s
		}
		s.tls = time.Since(t0)
		conn = tc
	}
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	t0 := time.Now()
	line, err := bufio.NewReader(conn).ReadString('\n')
	s.ttfb = time.Since(t0)
	s.total = time.Since(start)
	if err != nil {
		s.err = fmt.Errorf("connected, but no banner: %w", err)
		return s
	}
	s.banner = strings.TrimSpace(line)
	if len(s.banner) > 40 {
		s.banner = s.banner[:40] + "…"
	}
	return s
}

func shortErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 90 {
		msg = msg[:90] + "…"
	}
	return msg
}

// S3Config holds credentials for the throughput test.
type S3Config struct {
	Endpoint, Region, Bucket, AccessKey, SecretKey string
	SizeMB                                         int
}

// S3FromEnv reads S3_ENDPOINT, S3_REGION, S3_BUCKET, S3_ACCESS_KEY, S3_SECRET_KEY.
func S3FromEnv() (S3Config, bool) {
	c := S3Config{
		Endpoint: os.Getenv("S3_ENDPOINT"), Region: os.Getenv("S3_REGION"), Bucket: os.Getenv("S3_BUCKET"),
		AccessKey: os.Getenv("S3_ACCESS_KEY"), SecretKey: os.Getenv("S3_SECRET_KEY"), SizeMB: 8,
	}
	return c, c.Endpoint != "" && c.Bucket != "" && c.AccessKey != "" && c.SecretKey != ""
}

// RunS3 uploads, downloads and deletes one test object, printing throughput.
// Wasabi bills deleted objects for 90 days; an 8 MB object costs nothing noticeable.
func RunS3(ctx context.Context, w io.Writer, c S3Config) error {
	endpoint := strings.TrimPrefix(strings.TrimPrefix(c.Endpoint, "https://"), "http://")
	cl, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""),
		Secure: !strings.HasPrefix(c.Endpoint, "http://"),
		Region: c.Region,
	})
	if err != nil {
		return err
	}
	data := make([]byte, c.SizeMB<<20)
	rand.Read(data)
	key := fmt.Sprintf("observer-netcheck/%d.bin", time.Now().UnixNano())

	t0 := time.Now()
	if _, err := cl.PutObject(ctx, c.Bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"}); err != nil {
		return fmt.Errorf("put: %w", err)
	}
	up := time.Since(t0)

	t0 = time.Now()
	obj, err := cl.GetObject(ctx, c.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	n, err := io.Copy(io.Discard, obj)
	obj.Close()
	down := time.Since(t0)
	if err != nil {
		return fmt.Errorf("get read: %w", err)
	}
	if err := cl.RemoveObject(ctx, c.Bucket, key, minio.RemoveObjectOptions{}); err != nil {
		fmt.Fprintf(w, "warning: could not delete %s: %v\n", key, err)
	}
	mbps := func(bytes int64, d time.Duration) float64 { return float64(bytes) / (1 << 20) / d.Seconds() }
	fmt.Fprintf(w, "s3 %s/%s: upload %d MB in %s (%.1f MB/s), download in %s (%.1f MB/s)\n",
		endpoint, c.Bucket, c.SizeMB, up.Round(time.Millisecond), mbps(int64(len(data)), up),
		down.Round(time.Millisecond), mbps(n, down))
	return nil
}
