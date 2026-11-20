// Package config loads service configuration from environment variables.
package config

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"

	"github.com/MamaShip/Observer/internal/auth"
	"github.com/MamaShip/Observer/internal/blob"
	"github.com/MamaShip/Observer/internal/mail"
	"github.com/MamaShip/Observer/internal/notify"
	"github.com/MamaShip/Observer/internal/snapserver"
	"github.com/MamaShip/Observer/internal/watch"
	"github.com/MamaShip/Observer/internal/wechat"
)

// Config is the configuration of `observer serve` and the commands that
// need the database. Fields are added as milestones need them (plan.md §13.3).
type Config struct {
	DatabaseURL string `env:"DATABASE_URL,required,notEmpty"`
	DBMaxConns  int32  `env:"DB_MAX_CONNS" envDefault:"10"`

	HTTPAddr string `env:"HTTP_ADDR" envDefault:":8080"`
	BaseURL  string `env:"BASE_URL" envDefault:"http://localhost:8080"`
	// SnapBaseURL must be a different host from BaseURL: snapshots are
	// third-party content and must not share the site's origin.
	SnapBaseURL       string `env:"SNAP_BASE_URL" envDefault:"http://127.0.0.1:8080"`
	SigningKey        string `env:"SIGNING_KEY"` // HMAC key for snapshot links, at least 32 characters
	TrustProxyHeaders bool   `env:"TRUST_PROXY_HEADERS"`
	ServiceName       string `env:"SERVICE_NAME" envDefault:"折光观察者"`

	RegistrationMode    string `env:"REGISTRATION_MODE" envDefault:"invite"` // open or invite
	MaxOpenWatches      int    `env:"MAX_OPEN_WATCHES" envDefault:"50"`
	MaxDailySubmissions int    `env:"MAX_DAILY_SUBMISSIONS" envDefault:"30"`

	// Email. Without SMTP_HOST, messages are only logged (development).
	SMTPHost        string   `env:"SMTP_HOST"`
	SMTPPort        int      `env:"SMTP_PORT" envDefault:"587"`
	SMTPUser        string   `env:"SMTP_USER"`
	SMTPPassword    string   `env:"SMTP_PASSWORD"`
	MailFrom        string   `env:"MAIL_FROM"`
	DailyEmailLimit int      `env:"DAILY_EMAIL_LIMIT" envDefault:"100"`
	AdminEmails     []string `env:"ADMIN_EMAILS" envSeparator:","`

	CacheDir       string `env:"CACHE_DIR"` // local cache of snapshot files and images
	CacheMaxBytes  int64  `env:"CACHE_MAX_BYTES" envDefault:"5368709120"`
	HealthcheckURL string `env:"HEALTHCHECK_URL"`

	LogLevel  slog.Level `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat string     `env:"LOG_FORMAT" envDefault:"json"` // json or text

	// Object storage: S3 (Wasabi) when S3_BUCKET is set, else a local directory.
	S3Endpoint  string `env:"S3_ENDPOINT"`
	S3Region    string `env:"S3_REGION"`
	S3Bucket    string `env:"S3_BUCKET"`
	S3AccessKey string `env:"S3_ACCESS_KEY"`
	S3SecretKey string `env:"S3_SECRET_KEY"`
	BlobDir     string `env:"BLOB_DIR"`

	WatchDays        int           `env:"WATCH_DAYS" envDefault:"60"`
	FetchMinInterval time.Duration `env:"FETCH_MIN_INTERVAL" envDefault:"3s"`
	FetchUserAgent   string        `env:"FETCH_USER_AGENT" envDefault:"ios"` // preset name or full string
	CanaryAliveURL   string        `env:"CANARY_ALIVE_URL"`
	CanaryDeletedURL string        `env:"CANARY_DELETED_URL"`
}

// Load reads the configuration from the process environment.
func Load() (Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return Config{}, err
	}
	var errs []error
	if c.LogFormat != "json" && c.LogFormat != "text" {
		errs = append(errs, fmt.Errorf("LOG_FORMAT must be json or text, got %q", c.LogFormat))
	}
	if c.DBMaxConns < 2 {
		// River's leader election and the job fetcher each hold a connection.
		errs = append(errs, fmt.Errorf("DB_MAX_CONNS must be at least 2, got %d", c.DBMaxConns))
	}
	if c.S3Bucket != "" && (c.S3Endpoint == "" || c.S3AccessKey == "" || c.S3SecretKey == "") {
		errs = append(errs, errors.New("S3_BUCKET needs S3_ENDPOINT, S3_ACCESS_KEY and S3_SECRET_KEY"))
	}
	if c.S3Bucket == "" && c.BlobDir == "" {
		errs = append(errs, errors.New("set S3_BUCKET (production) or BLOB_DIR (development) for snapshot storage"))
	}
	if c.WatchDays < 1 || c.WatchDays > 365 {
		errs = append(errs, fmt.Errorf("WATCH_DAYS must be 1..365, got %d", c.WatchDays))
	}
	if c.FetchMinInterval < time.Second {
		errs = append(errs, fmt.Errorf("FETCH_MIN_INTERVAL must be at least 1s, got %s", c.FetchMinInterval))
	}
	base, err1 := parseOrigin(c.BaseURL)
	snap, err2 := parseOrigin(c.SnapBaseURL)
	switch {
	case err1 != nil:
		errs = append(errs, fmt.Errorf("BASE_URL: %w", err1))
	case err2 != nil:
		errs = append(errs, fmt.Errorf("SNAP_BASE_URL: %w", err2))
	case base.Host == snap.Host:
		errs = append(errs, errors.New("SNAP_BASE_URL must use a different host than BASE_URL (e.g. snap.<domain>)"))
	}
	if c.RegistrationMode != auth.ModeOpen && c.RegistrationMode != auth.ModeInvite {
		errs = append(errs, fmt.Errorf("REGISTRATION_MODE must be open or invite, got %q", c.RegistrationMode))
	}
	if c.SigningKey != "" && len(c.SigningKey) < 32 {
		errs = append(errs, errors.New("SIGNING_KEY must be at least 32 characters (try: openssl rand -hex 32)"))
	}
	for i, a := range c.AdminEmails {
		e, err := auth.NormalizeEmail(a)
		if err != nil {
			errs = append(errs, fmt.Errorf("ADMIN_EMAILS: %q is not an email address", a))
		}
		c.AdminEmails[i] = e
	}
	if c.SMTPHost != "" && c.MailFrom == "" {
		errs = append(errs, errors.New("SMTP_HOST needs MAIL_FROM"))
	}
	if c.DailyEmailLimit < 1 {
		errs = append(errs, errors.New("DAILY_EMAIL_LIMIT must be positive"))
	}
	if c.Production() {
		// Development conveniences that must not reach production.
		if c.SigningKey == "" {
			errs = append(errs, errors.New("SIGNING_KEY is required when BASE_URL is https"))
		}
		if c.SMTPHost == "" {
			errs = append(errs, errors.New("SMTP_HOST is required when BASE_URL is https"))
		}
		if snap.Scheme != "https" {
			errs = append(errs, errors.New("SNAP_BASE_URL must be https when BASE_URL is"))
		}
	}
	for _, u := range []string{c.CanaryAliveURL, c.CanaryDeletedURL} {
		if u == "" {
			continue
		}
		if _, err := wechat.ParseLink(u); err != nil {
			errs = append(errs, fmt.Errorf("canary URL %q: %w", u, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Production is true when the site is served over https.
func (c Config) Production() bool { return strings.HasPrefix(c.BaseURL, "https://") }

func parseOrigin(s string) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil {
		return nil, err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return nil, fmt.Errorf("%q must be an origin like https://example.com", s)
	}
	return u, nil
}

// Origin is BASE_URL without a trailing slash.
func (c Config) Origin() string { return strings.TrimRight(c.BaseURL, "/") }

// SnapOrigin is SNAP_BASE_URL without a trailing slash.
func (c Config) SnapOrigin() string { return strings.TrimRight(c.SnapBaseURL, "/") }

// SnapHost is the Host header that selects the snapshot origin.
func (c Config) SnapHost() string {
	u, _ := url.Parse(c.SnapBaseURL)
	return u.Host
}

// Signer returns the snapshot link signer. Without SIGNING_KEY (development
// only) it uses a random key, so links stop working after a restart.
func (c Config) Signer() (snapserver.Signer, bool, error) {
	if c.SigningKey != "" {
		s, err := snapserver.NewSigner([]byte(c.SigningKey))
		return s, false, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return snapserver.Signer{}, false, err
	}
	s, err := snapserver.NewSigner(key)
	return s, true, err
}

// Mailer returns the SMTP mailer, or a logging one without SMTP_HOST.
func (c Config) Mailer(log *slog.Logger) (mail.Mailer, error) {
	if c.SMTPHost == "" {
		return mail.NewMemory(log), nil
	}
	return mail.NewSMTP(mail.SMTPConfig{Host: c.SMTPHost, Port: c.SMTPPort, Username: c.SMTPUser, Password: c.SMTPPassword, From: c.MailFrom})
}

// Notify is the notifier configuration.
func (c Config) Notify() notify.Config {
	return notify.Config{ServiceName: c.ServiceName, BaseURL: c.Origin(), DailyLimit: c.DailyEmailLimit, AdminEmails: c.AdminEmails}
}

// Logger builds the process logger described by the configuration.
func (c Config) Logger() *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.LogLevel}
	if c.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

// Blob opens the configured object store.
func (c Config) Blob() (blob.Store, error) {
	if c.S3Bucket != "" {
		return blob.NewS3(blob.S3Config{
			Endpoint: c.S3Endpoint, Region: c.S3Region, Bucket: c.S3Bucket,
			AccessKey: c.S3AccessKey, SecretKey: c.S3SecretKey,
		})
	}
	return blob.NewDir(c.BlobDir)
}

// UserAgent is the User-Agent string to fetch with.
func (c Config) UserAgent() string {
	if ua, ok := wechat.UserAgents[c.FetchUserAgent]; ok {
		return ua
	}
	return c.FetchUserAgent
}

// Watch is the watch service configuration.
func (c Config) Watch() watch.Config {
	return watch.Config{
		WatchDays: c.WatchDays, MinFetchInterval: c.FetchMinInterval,
		CanaryAliveURL: c.CanaryAliveURL, CanaryDeletedURL: c.CanaryDeletedURL,
	}
}
