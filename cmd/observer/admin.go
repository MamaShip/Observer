package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MamaShip/Observer/internal/app"
	"github.com/MamaShip/Observer/internal/auth"
	"github.com/MamaShip/Observer/internal/blob"
	"github.com/MamaShip/Observer/internal/config"
	"github.com/MamaShip/Observer/internal/jobs"
	"github.com/MamaShip/Observer/internal/snapserver"
	"github.com/MamaShip/Observer/internal/store"
	"github.com/MamaShip/Observer/internal/store/db"
	"github.com/MamaShip/Observer/internal/watch"
)

const adminUsage = `Usage: observer admin <command>

  stats                            service counters
  invite <email>                   email a one-time registration link
  disable <email> | enable <email> block or unblock an account (disabling also logs it out)
  logout-all <email>               end every session of an account
  role <email> <admin|user>        give or take the admin console (/admin)
  email-limit <email> <n|default>  notification emails per month for one account
                                   (default: MONTHLY_EMAIL_LIMIT; 0: no limit)
  session <email>                  start a session and print its token (for smoke tests;
                                   end it with logout-all)
  submit -email ADDR <url>...      watch links as that user (created if new)
  articles [-n N]                  recent articles and their state
  check <article-id>               check an alive article at the next scheduler run
  export [-o FILE] <snapshot-id>   write a snapshot as one self-contained HTML file
  backup                           dump the database to object storage now
  restore -db URL [-date YYYY-MM-DD]
                                   load a backup (default: today's) into an empty database

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
	case "invite":
		if len(rest) != 1 {
			return errors.New("usage: observer admin invite <email>")
		}
		log := slog.Default()
		rc, err := jobs.NewInsertOnlyClient(pool, log)
		if err != nil {
			return err
		}
		svc := auth.New(pool, nil, auth.Config{Mode: cfg.RegistrationMode}, log)
		svc.SetJobs(rc)
		if err := svc.InviteFromCLI(ctx, rest[0]); err != nil {
			return err
		}
		fmt.Println("Invitation processed.")
		return nil
	case "disable", "enable", "logout-all":
		return adminUser(ctx, q, cmd, rest)
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
	case "role":
		if len(rest) != 2 || (rest[1] != "admin" && rest[1] != "user") {
			return errors.New("usage: observer admin role <email> <admin|user>")
		}
		email, err := auth.NormalizeEmail(rest[0])
		if err != nil {
			return err
		}
		n, err := q.SetUserRole(ctx, db.SetUserRoleParams{Email: email, Role: rest[1]})
		if err == nil && n == 0 {
			err = errors.New("no account with that email (observer admin invite creates one)")
		}
		if err == nil {
			fmt.Printf("%s is now %s\n", email, rest[1])
		}
		return err
	case "email-limit":
		if len(rest) != 2 {
			return errors.New("usage: observer admin email-limit <email> <n|default>")
		}
		email, err := auth.NormalizeEmail(rest[0])
		if err != nil {
			return err
		}
		var limit *int32
		if rest[1] != "default" {
			n, err := strconv.Atoi(rest[1])
			if err != nil || n < 0 {
				return errors.New("the limit is a number of emails (0: no limit) or default")
			}
			v := int32(n)
			limit = &v
		}
		n, err := q.SetMonthlyEmailLimit(ctx, db.SetMonthlyEmailLimitParams{Email: email, MonthlyEmailLimit: limit})
		if err == nil && n == 0 {
			err = errors.New("no account with that email")
		}
		if err == nil {
			fmt.Printf("%s: monthly notification limit %s\n", email, rest[1])
		}
		return err
	case "session":
		if len(rest) != 1 {
			return errors.New("usage: observer admin session <email>")
		}
		sess, err := auth.New(pool, nil, auth.Config{Mode: cfg.RegistrationMode}, slog.New(slog.DiscardHandler)).IssueSession(ctx, rest[0], "observer admin session")
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("no account with that email (observer admin invite creates one)")
		}
		if err != nil {
			return err
		}
		fmt.Println(sess.Token)
		return nil
	case "backup", "restore":
		return adminBackup(ctx, cfg, cmd, rest)
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

func adminUser(ctx context.Context, q *db.Queries, cmd string, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: observer admin %s <email>", cmd)
	}
	email, err := auth.NormalizeEmail(args[0])
	if err != nil {
		return err
	}
	u, err := q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("no account for %s", email)
	}
	if err != nil {
		return err
	}
	switch cmd {
	case "disable", "enable":
		status := map[string]string{"disable": "disabled", "enable": "active"}[cmd]
		if _, err := q.SetUserStatus(ctx, db.SetUserStatusParams{Email: email, Status: status}); err != nil {
			return err
		}
		fmt.Printf("%s is now %s\n", email, status)
		if cmd == "enable" {
			return nil
		}
	}
	n, err := q.DeleteUserSessions(ctx, u.ID)
	if err == nil {
		fmt.Printf("%d sessions ended\n", n)
	}
	return err
}

func adminSubmit(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, q *db.Queries, args []string) error {
	fs := flag.NewFlagSet("submit", flag.ExitOnError)
	email := fs.String("email", "", "user email (required)")
	fs.Parse(args)
	if *email == "" || fs.NArg() == 0 {
		return errors.New("usage: observer admin submit -email ADDR URL [URL ...]")
	}
	log := cfg.Logger()
	svc, _, err := app.NewService(cfg, pool, log, nil, nil)
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
		w, err := svc.Submit(ctx, user.ID, u, watch.Quota{})
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
	file, err := snapserver.SingleFile(ctx, q, bs, page)
	if err != nil {
		return err
	}
	if *out == "" {
		_, err = file.WriteTo(os.Stdout)
		return err
	}
	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	if _, err := file.WriteTo(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func readAll(ctx context.Context, s blob.Store, key string) ([]byte, error) {
	r, err := s.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	defer r.Close()
	return io.ReadAll(r)
}
