package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MamaShip/Observer/internal/app"
	"github.com/MamaShip/Observer/internal/blob"
	"github.com/MamaShip/Observer/internal/config"
	"github.com/MamaShip/Observer/internal/jobs"
	"github.com/MamaShip/Observer/internal/snapshot"
	"github.com/MamaShip/Observer/internal/store"
	"github.com/MamaShip/Observer/internal/store/db"
)

const adminUsage = `Usage: observer admin <command>

  stats                            service counters
  submit -email ADDR <url>...      watch links as that user (created if new)
  articles [-n N]                  recent articles and their state
  check <article-id>               check an alive article at the next scheduler run
  export [-o FILE] <snapshot-id>   write a snapshot as one self-contained HTML file

Jobs are queued for a running "observer serve" to execute.
`

func runAdmin(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, adminUsage)
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := store.Open(ctx, cfg.DatabaseURL, 4)
	if err != nil {
		return err
	}
	defer pool.Close()
	q := db.New(pool)

	switch cmd, rest := args[0], args[1:]; cmd {
	case "stats":
		return adminStats(ctx, q)
	case "submit":
		return adminSubmit(ctx, cfg, pool, q, rest)
	case "articles":
		return adminArticles(ctx, q, rest)
	case "check":
		if len(rest) != 1 {
			return errors.New("usage: observer admin check <article-id>")
		}
		id, err := uuid.Parse(rest[0])
		if err != nil {
			return err
		}
		n, err := q.RequestCheckNow(ctx, id)
		if err == nil && n == 0 {
			err = errors.New("no alive article with that id")
		}
		return err
	case "export":
		return adminExport(ctx, cfg, q, rest)
	default:
		fmt.Fprint(os.Stderr, adminUsage)
		os.Exit(2)
		return nil
	}
}

func adminStats(ctx context.Context, q *db.Queries) error {
	s, err := q.Stats(ctx)
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

func adminSubmit(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, q *db.Queries, args []string) error {
	fs := flag.NewFlagSet("submit", flag.ExitOnError)
	email := fs.String("email", "", "user email (required)")
	fs.Parse(args)
	if *email == "" || fs.NArg() == 0 {
		return errors.New("usage: observer admin submit -email ADDR URL [URL ...]")
	}
	log := cfg.Logger()
	svc, _, err := app.NewService(cfg, pool, log)
	if err != nil {
		return err
	}
	rc, err := jobs.NewInsertOnlyClient(pool, log)
	if err != nil {
		return err
	}
	svc.SetJobs(rc)
	user, err := q.UpsertUserByEmail(ctx, *email)
	if err != nil {
		return err
	}
	var failed error
	for _, u := range fs.Args() {
		w, err := svc.Submit(ctx, user.ID, u)
		if err != nil {
			fmt.Printf("%s\n  error: %v\n", u, err)
			failed = errors.New("some links were rejected")
			continue
		}
		fmt.Printf("%s\n  watch %s  %s", u, w.ID, w.Status)
		if w.EndReason != nil {
			fmt.Printf(" (%s)", *w.EndReason)
		}
		fmt.Printf("  article %s\n", w.ArticleID)
	}
	return failed
}

func adminArticles(ctx context.Context, q *db.Queries, args []string) error {
	fs := flag.NewFlagSet("articles", flag.ExitOnError)
	n := fs.Int("n", 20, "how many")
	fs.Parse(args)
	rows, err := q.ListArticles(ctx, int32(*n))
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tWATCHES\tSNAPS\tFAILS\tNEXT CHECK\tTITLE")
	for _, r := range rows {
		status := r.Status
		if r.Status == "deleted" && r.DeletedReasonCode != nil {
			status += "/" + *r.DeletedReasonCode
		} else if r.DeletionSuspectedAt != nil {
			status += " (deletion suspected)"
		}
		next := "-"
		if r.NextCheckAt != nil {
			next = time.Until(*r.NextCheckAt).Round(time.Minute).String()
		}
		title := []rune("")
		if r.Title != nil {
			title = []rune(*r.Title)
			title = title[:min(40, len(title))]
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%s\t%s\n", r.ID, status, r.ActiveWatches, r.SnapshotCount, r.ConsecutiveFailures, next, string(title))
	}
	return tw.Flush()
}

func adminExport(ctx context.Context, cfg config.Config, q *db.Queries, args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	out := fs.String("o", "", "output file (default: stdout)")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("usage: observer admin export [-o FILE] <snapshot-id>")
	}
	id, err := uuid.Parse(fs.Arg(0))
	if err != nil {
		return err
	}
	snap, err := q.GetSnapshot(ctx, id)
	if err != nil {
		return err
	}
	bs, err := cfg.Blob()
	if err != nil {
		return err
	}
	page, err := readAll(ctx, bs, snap.HtmlKey)
	if err != nil {
		return err
	}
	single, err := snapshot.Inline(page, func(sha string) ([]byte, string, error) {
		raw, err := hex.DecodeString(sha)
		if err != nil || len(raw) != 32 {
			return nil, "", fmt.Errorf("bad hash %q", sha)
		}
		asset, err := q.GetAsset(ctx, raw)
		if err != nil {
			return nil, "", err
		}
		data, err := readAll(ctx, bs, blob.AssetKey([32]byte(raw)))
		return data, asset.ContentType, err
	})
	if err != nil {
		return err
	}
	if *out == "" {
		_, err = os.Stdout.Write(single)
		return err
	}
	return os.WriteFile(*out, single, 0o644)
}

func readAll(ctx context.Context, s blob.Store, key string) ([]byte, error) {
	r, err := s.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	defer r.Close()
	return io.ReadAll(r)
}
