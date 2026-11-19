package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func setRequired(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@localhost/x")
	t.Setenv("BLOB_DIR", t.TempDir())
}

func TestLoadDefaults(t *testing.T) {
	setRequired(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":8080" || c.DBMaxConns != 10 || c.LogLevel != slog.LevelInfo || c.LogFormat != "json" ||
		c.WatchDays != 60 || c.FetchMinInterval != 3*time.Second || !strings.Contains(c.UserAgent(), "iPhone") {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := []struct {
		name, key, value, want string
	}{
		{"missing database", "DATABASE_URL", "", "DATABASE_URL"},
		{"bad log format", "LOG_FORMAT", "xml", "LOG_FORMAT"},
		{"bad log level", "LOG_LEVEL", "loud", "LogLevel"},
		{"too few connections", "DB_MAX_CONNS", "1", "DB_MAX_CONNS"},
		{"no storage", "BLOB_DIR", "", "BLOB_DIR"},
		{"incomplete S3", "S3_BUCKET", "observer", "S3_ENDPOINT"},
		{"fetching too fast", "FETCH_MIN_INTERVAL", "500ms", "FETCH_MIN_INTERVAL"},
		{"watch too long", "WATCH_DAYS", "400", "WATCH_DAYS"},
		{"bad canary", "CANARY_ALIVE_URL", "https://example.com/", "canary"},
		{"same host for snapshots", "SNAP_BASE_URL", "http://localhost:8080", "different host"},
		{"base URL with path", "BASE_URL", "https://example.com/app", "origin"},
		{"short signing key", "SIGNING_KEY", "short", "SIGNING_KEY"},
		{"bad mode", "REGISTRATION_MODE", "free", "REGISTRATION_MODE"},
		{"bad admin email", "ADMIN_EMAILS", "ops@example.com,nope", "ADMIN_EMAILS"},
		{"smtp without from", "SMTP_HOST", "smtp.example.com", "MAIL_FROM"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(tc.key, tc.value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error mentioning %s", err, tc.want)
			}
		})
	}
}

func TestLoadLevel(t *testing.T) {
	setRequired(t)
	t.Setenv("LOG_LEVEL", "debug")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != slog.LevelDebug {
		t.Fatalf("level = %v", c.LogLevel)
	}
}

// An https BASE_URL means production: dev fallbacks are refused.
func TestProductionRequirements(t *testing.T) {
	setRequired(t)
	t.Setenv("BASE_URL", "https://observer.example")
	t.Setenv("SNAP_BASE_URL", "http://snap.observer.example")
	_, err := Load()
	for _, want := range []string{"SIGNING_KEY is required", "SMTP_HOST is required", "SNAP_BASE_URL must be https"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error %v lacks %q", err, want)
		}
	}
	t.Setenv("SNAP_BASE_URL", "https://snap.observer.example")
	t.Setenv("SIGNING_KEY", strings.Repeat("k", 32))
	t.Setenv("SMTP_HOST", "smtp.resend.com")
	t.Setenv("MAIL_FROM", "折光观察者 <noreply@observer.example>")
	t.Setenv("ADMIN_EMAILS", " Ops@Example.com ")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.SnapHost() != "snap.observer.example" || c.AdminEmails[0] != "ops@example.com" || c.RegistrationMode != "invite" {
		t.Errorf("config = %+v", c)
	}
	if _, random, err := c.Signer(); err != nil || random {
		t.Errorf("signer: random=%v err=%v", random, err)
	}
}
