// Package app wires the service together for `observer serve`: database,
// migrations, River workers and the HTTP server, with graceful shutdown.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/MamaShip/Observer/internal/api"
	"github.com/MamaShip/Observer/internal/auth"
	"github.com/MamaShip/Observer/internal/backup"
	"github.com/MamaShip/Observer/internal/blob"
	"github.com/MamaShip/Observer/internal/config"
	"github.com/MamaShip/Observer/internal/jobs"
	"github.com/MamaShip/Observer/internal/mail"
	"github.com/MamaShip/Observer/internal/mp"
	"github.com/MamaShip/Observer/internal/notify"
	"github.com/MamaShip/Observer/internal/snapserver"
	"github.com/MamaShip/Observer/internal/store"
	"github.com/MamaShip/Observer/internal/store/db"
	"github.com/MamaShip/Observer/internal/watch"
	"github.com/MamaShip/Observer/internal/webui"
	"github.com/MamaShip/Observer/internal/wechat"
)

// ShutdownTimeout bounds how long in-flight requests and jobs get to finish.
const ShutdownTimeout = 15 * time.Second

// App is a configured, migrated service ready to Run.
type App struct {
	cfg     config.Config
	log     *slog.Logger
	pool    *pgxpool.Pool
	river   *river.Client[pgx.Tx]
	hb      *jobs.Heartbeat
	started time.Time
	handler http.Handler

	Watch  *watch.Service
	Notify *notify.Notifier
	Auth   *auth.Service
}

// Overrides replace external dependencies, for tests.
type Overrides struct {
	NewSession func() watch.Session // instead of real WeChat requests
	Mailer     mail.Mailer          // instead of the configured mailer
}

// New connects to the database, applies migrations and builds the workers
// and HTTP handler. Call Close when done, whether or not Run was called.
func New(ctx context.Context, cfg config.Config, log *slog.Logger, version string, o ...Overrides) (*App, error) {
	var ov Overrides
	if len(o) > 0 {
		ov = o[0]
	}
	pool, err := store.Open(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return nil, err
	}
	a, err := build(ctx, cfg, log, version, pool, ov)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return a, nil
}

func build(ctx context.Context, cfg config.Config, log *slog.Logger, version string, pool *pgxpool.Pool, ov Overrides) (*App, error) {
	if err := store.Migrate(ctx, pool, log); err != nil {
		return nil, err
	}
	mailer := ov.Mailer
	if mailer == nil {
		m, err := cfg.Mailer(log)
		if err != nil {
			return nil, err
		}
		if cfg.SMTPHost == "" {
			log.Warn("SMTP_HOST not set: emails are written to the log instead of being sent")
		}
		mailer = m
	}
	notifier := notify.New(pool, mailer, cfg.Notify(), log)
	svc, store, err := NewService(cfg, pool, log, notifier, ov.NewSession)
	if err != nil {
		return nil, err
	}
	authSvc := auth.New(pool, notifier, auth.Config{Mode: cfg.RegistrationMode, LinkKey: []byte(cfg.SigningKey)}, log)

	hb := &jobs.Heartbeat{}
	var backups *backup.Service
	if cfg.S3Bucket != "" {
		// Production only: dumps go next to the snapshots. (Development has
		// no matching pg_dump and nowhere useful to put them.)
		backups = backup.New(backup.Config{DatabaseURL: cfg.DatabaseURL}, store, notifier, log)
	}
	rc, err := jobs.NewClient(pool, log, hb, jobs.Services{Watch: svc, Notify: notifier, Auth: authSvc, Backup: backups, HealthcheckURL: cfg.HealthcheckURL})
	if err != nil {
		return nil, fmt.Errorf("river: %w", err)
	}

	signer, random, err := cfg.Signer()
	if err != nil {
		return nil, err
	}
	if random {
		log.Warn("SIGNING_KEY not set: using a random key; snapshot links stop working after a restart")
	}
	snapStore := store
	if cfg.CacheDir != "" {
		if snapStore, err = blob.NewCache(store, cfg.CacheDir, cfg.CacheMaxBytes); err != nil {
			return nil, fmt.Errorf("cache: %w", err)
		}
	}
	q := db.New(pool)
	snap := snapserver.New(q, snapStore, signer, cfg.Origin(), log)

	a := &App{cfg: cfg, log: log, pool: pool, river: rc, hb: hb, started: time.Now(), Watch: svc, Notify: notifier, Auth: authSvc}
	var bot *mp.Bot
	var toolkit *api.SiteToolkit
	if cfg.ToolkitURL != "" {
		toolkit = &api.SiteToolkit{Name: cfg.ToolkitName, URL: cfg.ToolkitURL}
	}
	var siteWechat *api.SiteWechat
	wechatHandler := http.NotFoundHandler()
	if cfg.Wechat() {
		bot = mp.NewBot(authSvc, svc, q, mp.Config{BaseURL: cfg.Origin(), ServiceName: cfg.ServiceName, AccountName: cfg.WechatName,
			InviteOnly: cfg.RegistrationMode == auth.ModeInvite, WatchDays: cfg.WatchDays,
			MaxOpen: cfg.MaxOpenWatches, MaxDaily: cfg.MaxDailySubmissions, ResolveWait: 3 * time.Second})
		wechatHandler = mp.NewHandler(bot, cfg.WechatToken, cfg.WechatAccountID, log)
		siteWechat = &api.SiteWechat{Name: cfg.WechatName}
	}
	apiHandler, _ := api.New(api.Deps{
		Version: version, Health: a, StallAfter: jobs.StallAfter,
		Auth: authSvc, Notify: notifier, Watch: svc, Queries: q, Signer: signer,
		BaseURL: cfg.Origin(), SnapBaseURL: cfg.SnapOrigin(), TrustProxy: cfg.TrustProxyHeaders,
		Limits: api.Limits{MaxOpenWatches: cfg.MaxOpenWatches, MaxDailySubmission: cfg.MaxDailySubmissions, DailyEmails: cfg.DailyEmailLimit,
			MonthlyInvites: cfg.MonthlyInviteLimit},
		Site: api.Site{ServiceName: cfg.ServiceName, RegistrationMode: cfg.RegistrationMode, WatchDays: cfg.WatchDays, MaxOpenWatches: cfg.MaxOpenWatches,
			MonthlyEmails: cfg.MonthlyEmailLimit, Wechat: siteWechat, Toolkit: toolkit},
		MP: bot,
	})
	web := webui.Handler(webui.CSP(cfg.SnapOrigin()))
	main := siteHeaders(cfg.Production(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == api.Prefix || strings.HasPrefix(r.URL.Path, api.Prefix+"/") {
			apiHandler.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == mp.Path {
			wechatHandler.ServeHTTP(w, r)
			return
		}
		web.ServeHTTP(w, r)
	}))
	snapHost, snapHandler := cfg.SnapHost(), snap.Handler()
	a.handler = recoverer(log, accessLog(log, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == snapHost {
			snapHandler.ServeHTTP(w, r)
			return
		}
		main.ServeHTTP(w, r)
	})))
	return a, nil
}

// siteHeaders adds the main site's security headers. Snapshot pages set
// their own (they must be frameable by the main site).
func siteHeaders(https bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY")
		if https {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// NewService builds the watch service and its object store. The caller sets
// its job queue (jobs.NewClient does). alerter and newSession may be nil.
func NewService(cfg config.Config, pool *pgxpool.Pool, log *slog.Logger, alerter watch.Alerter, newSession func() watch.Session) (*watch.Service, blob.Store, error) {
	store, err := cfg.Blob()
	if err != nil {
		return nil, nil, fmt.Errorf("object storage: %w", err)
	}
	if newSession == nil {
		ua := cfg.UserAgent()
		newSession = func() watch.Session { return wechat.NewSession(wechat.Options{UserAgent: ua}) }
	}
	svc := watch.New(watch.Deps{Pool: pool, Blob: store, Log: log, Config: cfg.Watch(), NewSession: newSession, Alerter: alerter})
	return svc, store, nil
}

// Handler is the root HTTP handler.
func (a *App) Handler() http.Handler { return a.handler }

// Run starts the workers and serves HTTP on ln until ctx is cancelled, then
// shuts both down gracefully.
func (a *App) Run(ctx context.Context, ln net.Listener) error {
	// River would hard-stop when its start context is cancelled; detach it so
	// the graceful Stop below decides how running jobs end.
	if err := a.river.Start(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("start river: %w", err)
	}
	srv := &http.Server{
		Handler:           a.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(a.log.Handler(), slog.LevelWarn),
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	a.log.Info("serving", "addr", ln.Addr().String())

	var err error
	select {
	case <-ctx.Done():
	case err = <-serveErr:
		err = fmt.Errorf("http server: %w", err)
	}

	a.log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownTimeout)
	defer cancel()
	if e := srv.Shutdown(shutdownCtx); e != nil && !errors.Is(e, http.ErrServerClosed) {
		a.log.Warn("http shutdown", "err", e)
	}
	if e := a.river.Stop(shutdownCtx); e != nil {
		a.log.Warn("river did not stop in time, cancelling running jobs", "err", e)
		hardCtx, hardCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer hardCancel()
		_ = a.river.StopAndCancel(hardCtx)
	}
	return err
}

// Close releases the database pool.
func (a *App) Close() { a.pool.Close() }

// PingDB implements api.HealthChecker.
func (a *App) PingDB(ctx context.Context) error { return a.pool.Ping(ctx) }

// LastHeartbeat implements api.HealthChecker.
func (a *App) LastHeartbeat() time.Time { return a.hb.Last() }

// Started implements api.HealthChecker.
func (a *App) Started() time.Time { return a.started }

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// accessLog logs one line per request. Query strings are left out: they will
// carry signatures and tokens.
func accessLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		level := slog.LevelInfo
		if rec.status >= 500 {
			level = slog.LevelWarn
		}
		log.LogAttrs(r.Context(), level, "http",
			slog.String("method", r.Method), slog.String("path", r.URL.Path),
			slog.Int("status", rec.status), slog.Int64("bytes", rec.bytes),
			slog.Duration("duration", time.Since(start)))
	})
}

// recoverer turns a handler panic into a 500 and a logged stack trace.
func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does
					panic(v)
				}
				log.Error("panic in handler", "path", r.URL.Path, "panic", v, "stack", string(debug.Stack()))
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
