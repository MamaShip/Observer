// Package config loads service configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/caarlos0/env/v11"

	"github.com/MamaShip/Observer/internal/blob"
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
