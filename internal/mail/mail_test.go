package mail

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeSMTP is a minimal SMTP server: no TLS, no auth.
type fakeSMTP struct {
	ln       net.Listener
	mu       sync.Mutex
	messages []string
	reject   string // reply to MAIL FROM, e.g. "550 daily quota exceeded"
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	say := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
	say("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250-fake")
			say("250 8BITMIME")
		case strings.HasPrefix(cmd, "MAIL FROM"):
			f.mu.Lock()
			rej := f.reject
			f.mu.Unlock()
			if rej != "" {
				say(rej)
				continue
			}
			say("250 OK")
		case strings.HasPrefix(cmd, "RCPT TO"):
			say("250 OK")
		case cmd == "DATA":
			say("354 go ahead")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.messages = append(f.messages, b.String())
			f.mu.Unlock()
			say("250 OK queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		case cmd == "RSET", cmd == "NOOP":
			say("250 OK")
		default:
			say("502 not implemented")
		}
	}
}

func TestSMTPSend(t *testing.T) {
	f := newFakeSMTP(t)
	host, port, _ := net.SplitHostPort(f.ln.Addr().String())
	p, _ := strconv.Atoi(port)
	s, err := NewSMTP(SMTPConfig{Host: host, Port: p, From: "折光观察者 <noreply@example.com>", AllowPlaintext: true})
	if err != nil {
		t.Fatal(err)
	}
	m, err := Render("login", "user@example.com", LoginData{Link: "https://example.com/login/verify#token=abc", ValidMinutes: 15, ServiceName: "折光观察者", ServiceURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Send(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Error("no Message-ID returned")
	}
	f.mu.Lock()
	raw := strings.Join(f.messages, "")
	f.mu.Unlock()
	for _, want := range []string{"To: <user@example.com>", "multipart/alternative", "text/plain", "text/html", id} {
		if !strings.Contains(raw, want) {
			t.Errorf("message lacks %q:\n%s", want, raw)
		}
	}

	f.mu.Lock()
	f.reject = "550 5.7.1 daily quota exceeded"
	f.mu.Unlock()
	if _, err := s.Send(context.Background(), m); !errors.Is(err, ErrQuota) {
		t.Fatalf("quota reply gave %v, want ErrQuota", err)
	}
}

func TestNewSMTPValidates(t *testing.T) {
	if _, err := NewSMTP(SMTPConfig{Host: "smtp.example.com", From: "not an address"}); err == nil {
		t.Error("bad From accepted")
	}
	if _, err := NewSMTP(SMTPConfig{From: "a@example.com"}); err == nil {
		t.Error("missing host accepted")
	}
}

func TestRender(t *testing.T) {
	m, err := Render("login", "u@example.com", LoginData{Link: "https://x.example/login/verify#token=t<>", ValidMinutes: 15, ServiceName: "折光观察者", ServiceURL: "https://x.example"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "折光观察者 登录链接" || !strings.Contains(m.Text, "https://x.example/login/verify#token=t<>") ||
		!strings.Contains(m.HTML, "#token=t%3c%3e") {
		t.Errorf("login message = %+v", m)
	}

	d, err := Render("digest", "u@example.com", DigestData{
		Deleted: 1, Expired: 1, ServiceName: "折光观察者", ServiceURL: "https://x.example", SettingsURL: "https://x.example/settings",
		Items: []DigestItem{
			{Title: "<script>文章一</script>", AccountName: "某公众号", Deleted: true, Reason: "该内容已被发布者删除", Link: "https://x.example/watches/1"},
			{Title: "文章二", Link: "https://x.example/watches/2"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Subject != "你观察的 1 篇文章已被删除" {
		t.Errorf("subject = %q", d.Subject)
	}
	for _, want := range []string{"【已删除】<script>文章一</script>（某公众号）", "微信提示：该内容已被发布者删除", "【观察期结束】文章二", "https://x.example/watches/2"} {
		if !strings.Contains(d.Text, want) {
			t.Errorf("digest text lacks %q:\n%s", want, d.Text)
		}
	}
	if strings.Contains(d.HTML, "<script>") || !strings.Contains(d.HTML, "&lt;script&gt;") {
		t.Error("digest HTML does not escape titles")
	}

	a, err := Render("alert", "ops@example.com", AlertData{Subject: "金丝雀检测失败", Body: "详情"})
	if err != nil || a.Subject != "[Observer 告警] 金丝雀检测失败" || a.HTML != "" {
		t.Errorf("alert = %+v, %v", a, err)
	}
}
