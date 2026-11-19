// Package config loads service configuration from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/caarlos0/env/v11"
)

// Config is the configuration of `observer serve` and the commands that
// need the database. Fields are added as milestones need them (plan.md §13.3).
type Config struct {
	DatabaseURL string `env:"DATABASE_URL,required,notEmpty"`
	DBMaxConns  int32  `env:"DB_MAX_CONNS" envDefault:"10"`

	HTTPAddr string `env:"HTTP_ADDR" envDefault:":8080"`
	BaseURL  string `env:"BASE_URL" envDefault:"http://localhost:8080"`

	LogLevel  slog.Level `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat string     `env:"LOG_FORMAT" envDefault:"json"` // json or text
}

// Load reads the configuration from the process environment.
func Load() (Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return Config{}, err
	}
	if c.LogFormat != "json" && c.LogFormat != "text" {
		return Config{}, fmt.Errorf("LOG_FORMAT must be json or text, got %q", c.LogFormat)
	}
	if c.DBMaxConns < 2 {
		// River's leader election and the job fetcher each hold a connection.
		return Config{}, fmt.Errorf("DB_MAX_CONNS must be at least 2, got %d", c.DBMaxConns)
	}
	return c, nil
}

// Logger builds the process logger described by the configuration.
func (c Config) Logger() *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.LogLevel}
	if c.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}
