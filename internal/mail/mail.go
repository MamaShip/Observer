// Package mail sends email: an SMTP transport for production (Resend or any
// other provider, plan.md §4) and an in-memory one for development and tests.
package mail

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	netmail "net/mail"
	"strings"
	"sync"
	"time"

	gomail "github.com/wneessen/go-mail"
)

// Message is one email. HTML is optional; Text is always sent.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Mailer sends messages and returns the Message-ID it used.
type Mailer interface {
	Send(ctx context.Context, m Message) (messageID string, err error)
}

// ErrQuota means the provider refused because a sending limit was reached;
// retrying today is pointless.
var ErrQuota = errors.New("mail: sending quota reached")

// SMTPConfig configures the SMTP transport.
type SMTPConfig struct {
	Host     string
	Port     int // 465 uses implicit TLS; anything else requires STARTTLS
	Username string
	Password string
	From     string // e.g. "折光观察者 <noreply@example.com>"

	// AllowPlaintext permits a server without TLS (tests only).
	AllowPlaintext bool
}

// SMTP sends through an SMTP server, one connection per message (volumes
// are tiny; no pool to go stale).
type SMTP struct {
	cfg    SMTPConfig
	opts   []gomail.Option
	domain string // of the From address, for Message-IDs
}

// NewSMTP validates the configuration. It does not connect.
func NewSMTP(c SMTPConfig) (*SMTP, error) {
	if c.Host == "" || c.From == "" {
		return nil, errors.New("mail: SMTP host and from address are required")
	}
	if c.Port == 0 {
		c.Port = 587
	}
	opts := []gomail.Option{gomail.WithPort(c.Port), gomail.WithTimeout(20 * time.Second)}
	switch {
	case c.AllowPlaintext:
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	case c.Port == 465:
		opts = append(opts, gomail.WithSSL())
	default:
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	}
	if c.Username != "" {
		opts = append(opts, gomail.WithSMTPAuth(gomail.SMTPAuthPlain),
			gomail.WithUsername(c.Username), gomail.WithPassword(c.Password))
	}
	// Fail early on a malformed From.
	from, err := netmail.ParseAddress(c.From)
	if err != nil {
		return nil, fmt.Errorf("mail: from address: %w", err)
	}
	domain := from.Address[strings.LastIndex(from.Address, "@")+1:]
	return &SMTP{cfg: c, opts: opts, domain: domain}, nil
}

// Send implements Mailer.
func (s *SMTP) Send(ctx context.Context, m Message) (string, error) {
	msg, err := build(s.cfg.From, s.domain, m)
	if err != nil {
		return "", err
	}
	client, err := gomail.NewClient(s.cfg.Host, s.opts...)
	if err != nil {
		return "", err
	}
	id := strings.Join(msg.GetGenHeader(gomail.HeaderMessageID), "")
	if err := client.DialAndSendWithContext(ctx, msg); err != nil {
		if isQuota(err) {
			return id, fmt.Errorf("%w: %w", ErrQuota, err)
		}
		return id, err
	}
	return id, nil
}

// Check connects and logs in without sending anything, to verify the
// configuration and credentials.
func (s *SMTP) Check(ctx context.Context) error {
	client, err := gomail.NewClient(s.cfg.Host, s.opts...)
	if err != nil {
		return err
	}
	if err := client.DialWithContext(ctx); err != nil {
		return err
	}
	return client.Close()
}

// build makes the MIME message, plain text first with an HTML alternative.
// The Message-ID uses the sender's domain rather than the machine's host
// name, which spam filters would see as a mismatch.
func build(from, domain string, m Message) (*gomail.Msg, error) {
	msg := gomail.NewMsg()
	if err := msg.From(from); err != nil {
		return nil, err
	}
	if err := msg.To(m.To); err != nil {
		return nil, err
	}
	msg.Subject(m.Subject)
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	msg.SetMessageIDWithValue(hex.EncodeToString(id) + "@" + domain)
	msg.SetDate()
	msg.SetBodyString(gomail.TypeTextPlain, m.Text)
	if m.HTML != "" {
		msg.AddAlternativeString(gomail.TypeTextHTML, m.HTML)
	}
	return msg, nil
}

// isQuota recognizes providers' daily or rate limit replies.
func isQuota(err error) bool {
	s := strings.ToLower(err.Error())
	for _, w := range []string{"quota", "daily limit", "rate limit", "too many", "limit exceeded"} {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// Memory keeps sent messages in memory and logs them, for development
// (the login link shows up in the log) and tests.
type Memory struct {
	mu   sync.Mutex
	sent []Message
	log  *slog.Logger
	err  error
}

// NewMemory returns a Memory mailer that logs each message to log (may be nil).
func NewMemory(log *slog.Logger) *Memory { return &Memory{log: log} }

// Send implements Mailer.
func (m *Memory) Send(_ context.Context, msg Message) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return "", m.err
	}
	m.sent = append(m.sent, msg)
	if m.log != nil {
		m.log.Info("email (not sent: no SMTP configured)", "to", msg.To, "subject", msg.Subject, "text", msg.Text)
	}
	return fmt.Sprintf("<memory-%d@observer>", len(m.sent)), nil
}

// Sent returns the messages sent so far.
func (m *Memory) Sent() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Message(nil), m.sent...)
}

// FailWith makes later sends fail with err (nil restores).
func (m *Memory) FailWith(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}
