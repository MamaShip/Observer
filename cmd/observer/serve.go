package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/MamaShip/Observer/internal/api"
	"github.com/MamaShip/Observer/internal/app"
	"github.com/MamaShip/Observer/internal/config"
	"github.com/MamaShip/Observer/internal/store"
	"github.com/MamaShip/Observer/internal/store/db"
)

func runServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := cfg.Logger()
	log.Info("starting", "version", version)

	a, err := app.New(ctx, cfg, log, version)
	if err != nil {
		return err
	}
	defer a.Close()

	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return err
	}
	return a.Run(ctx, ln)
}

func runMigrate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprintln(fs.Output(), "Usage: observer migrate [up|status]") }
	fs.Parse(args)
	cmd := fs.Arg(0)
	if cmd == "" {
		cmd = "up"
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := cfg.Logger()
	pool, err := store.Open(ctx, cfg.DatabaseURL, 2)
	if err != nil {
		return err
	}
	defer pool.Close()

	switch cmd {
	case "up":
		return store.Migrate(ctx, pool, log)
	case "status":
		p, closeDB, err := store.Migrations(pool)
		if err != nil {
			return err
		}
		defer closeDB()
		statuses, err := p.Status(ctx)
		if err != nil {
			return err
		}
		for _, s := range statuses {
			applied := "pending"
			if !s.AppliedAt.IsZero() {
				applied = s.AppliedAt.Format(time.RFC3339)
			}
			source := s.Source.Path
			if source == "" {
				source = "(Go migration)"
			}
			fmt.Printf("%05d  %-20s  %s\n", s.Source.Version, applied, source)
		}
		return nil
	default:
		fs.Usage()
		os.Exit(2)
		return nil
	}
}

// runHealthcheck is the Docker HEALTHCHECK: the distroless image has no curl.
func runHealthcheck(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("healthcheck", flag.ExitOnError)
	url := fs.String("url", "", "health endpoint (default: derived from HTTP_ADDR)")
	fs.Parse(args)
	if *url == "" {
		addr := os.Getenv("HTTP_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return fmt.Errorf("HTTP_ADDR %q: %w", addr, err)
		}
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		*url = "http://" + net.JoinHostPort(host, port) + api.Prefix + "/healthz"
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, *url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: %s %s", resp.Status, body)
	}
	return nil
}

func runAdmin(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "stats" {
		return errors.New("usage: observer admin stats")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := store.Open(ctx, cfg.DatabaseURL, 2)
	if err != nil {
		return err
	}
	defer pool.Close()

	s, err := db.New(pool).Stats(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("users            %d\n", s.Users)
	fmt.Printf("active watches   %d\n", s.ActiveWatches)
	fmt.Printf("articles         %d (deleted %d)\n", s.Articles, s.DeletedArticles)
	fmt.Printf("snapshots        %d\n", s.Snapshots)
	fmt.Printf("checks (24h)     %d (blocked %d)\n", s.Checks24h, s.Blocked24h)
	return nil
}
