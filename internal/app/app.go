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
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/MamaShip/Observer/internal/api"
	"github.com/MamaShip/Observer/internal/config"
	"github.com/MamaShip/Observer/internal/jobs"
	"github.com/MamaShip/Observer/internal/store"
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
}

// New connects to the database, applies migrations and builds the workers
// and HTTP handler. Call Close when done, whether or not Run was called.
func New(ctx context.Context, cfg config.Config, log *slog.Logger, version string) (*App, error) {
	pool, err := store.Open(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(ctx, pool, log); err != nil {
		pool.Close()
		return nil, err
	}
	hb := &jobs.Heartbeat{}
	rc, err := jobs.NewClient(pool, log, hb)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("river: %w", err)
	}
	a := &App{cfg: cfg, log: log, pool: pool, river: rc, hb: hb, started: time.Now()}

	mux := http.NewServeMux()
	api.New(mux, api.Deps{Version: version, Health: a, StallAfter: jobs.StallAfter})
	a.handler = recoverer(log, accessLog(log, mux))
	return a, nil
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
