package config

import (
	"log/slog"
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@localhost/x")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":8080" || c.DBMaxConns != 10 || c.LogLevel != slog.LevelInfo || c.LogFormat != "json" {
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://x@localhost/x")
			t.Setenv(tc.key, tc.value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error mentioning %s", err, tc.want)
			}
		})
	}
}

func TestLoadLevel(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@localhost/x")
	t.Setenv("LOG_LEVEL", "debug")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != slog.LevelDebug {
		t.Fatalf("level = %v", c.LogLevel)
	}
}
