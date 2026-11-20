// Package jobs runs the background work on River: thin workers that call
// the watch service, the periodic jobs, and the client that ties them up.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/MamaShip/Observer/internal/auth"
	"github.com/MamaShip/Observer/internal/notify"
	"github.com/MamaShip/Observer/internal/watch"
)

// HeartbeatInterval is how often the heartbeat job runs. /healthz reports the
// worker as stalled when no heartbeat arrived for StallAfter.
const (
	HeartbeatInterval = time.Minute
	StallAfter        = 5 * HeartbeatInterval
)

// Heartbeat records when the worker last ran a job end to end
// (scheduled by the leader, fetched from Postgres, executed).
type Heartbeat struct{ last atomic.Int64 }

// Beat records a heartbeat at t.
func (h *Heartbeat) Beat(t time.Time) { h.last.Store(t.UnixNano()) }

// Last returns the time of the last heartbeat, or the zero time if none yet.
func (h *Heartbeat) Last() time.Time {
	n := h.last.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

// HeartbeatArgs is the periodic job that proves the queue is working.
type HeartbeatArgs struct{}

// Kind implements river.JobArgs.
func (HeartbeatArgs) Kind() string { return "heartbeat" }

// InsertOpts keeps a backlog of heartbeats from piling up if the worker stalls.
func (HeartbeatArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByPeriod: HeartbeatInterval}}
}

type heartbeatWorker struct {
	river.WorkerDefaults[HeartbeatArgs]
	hb *Heartbeat
}

func (w *heartbeatWorker) Work(context.Context, *river.Job[HeartbeatArgs]) error {
	w.hb.Beat(time.Now())
	return nil
}

func (w *heartbeatWorker) Timeout(*river.Job[HeartbeatArgs]) time.Duration { return 10 * time.Second }

// result maps the watch service's errors onto River's job outcomes.
func result(err error) error {
	var snooze *watch.SnoozeError
	var retry *notify.RetryAt
	switch {
	case err == nil:
		return nil
	case errors.As(err, &snooze):
		return river.JobSnooze(max(snooze.After, 0))
	case errors.As(err, &retry):
		// JobSnooze panics on a negative duration; a time already past
		// means run again now.
		return river.JobSnooze(max(time.Until(retry.Time), 0))
	case errors.Is(err, watch.ErrPermanent):
		return river.JobCancel(err)
	default:
		return err
	}
}

// Fetch jobs may wait for the global rate limiter behind other fetches,
// so their timeout is generous.
const fetchTimeout = 10 * time.Minute

type resolveWorker struct {
	river.WorkerDefaults[watch.ResolveArgs]
	svc *watch.Service
}

func (w *resolveWorker) Work(ctx context.Context, job *river.Job[watch.ResolveArgs]) error {
	return result(w.svc.Resolve(ctx, job.Args, job.Attempt >= job.MaxAttempts))
}

func (w *resolveWorker) Timeout(*river.Job[watch.ResolveArgs]) time.Duration { return fetchTimeout }

type checkWorker struct {
	river.WorkerDefaults[watch.CheckArgs]
	svc *watch.Service
}

func (w *checkWorker) Work(ctx context.Context, job *river.Job[watch.CheckArgs]) error {
	return result(w.svc.Check(ctx, job.Args))
}

func (w *checkWorker) Timeout(*river.Job[watch.CheckArgs]) time.Duration { return fetchTimeout }

type snapshotWorker struct {
	river.WorkerDefaults[watch.SnapshotArgs]
	svc *watch.Service
}

func (w *snapshotWorker) Work(ctx context.Context, job *river.Job[watch.SnapshotArgs]) error {
	return result(w.svc.Snapshot(ctx, job.Args))
}

// A snapshot downloads up to 300 images one after another.
func (w *snapshotWorker) Timeout(*river.Job[watch.SnapshotArgs]) time.Duration {
	return 20 * time.Minute
}

// periodicWorker runs a function for a periodic job kind.
type periodicWorker[T river.JobArgs] struct {
	river.WorkerDefaults[T]
	run     func(context.Context) error
	timeout time.Duration
}

func (w *periodicWorker[T]) Work(ctx context.Context, _ *river.Job[T]) error {
	return result(w.run(ctx))
}

func (w *periodicWorker[T]) Timeout(*river.Job[T]) time.Duration { return w.timeout }

type digestWorker struct {
	river.WorkerDefaults[notify.DigestArgs]
	n *notify.Notifier
}

func (w *digestWorker) Work(ctx context.Context, job *river.Job[notify.DigestArgs]) error {
	return result(w.n.SendDigest(ctx, job.Args.UserID))
}

type alertWorker struct {
	river.WorkerDefaults[notify.AlertArgs]
	n *notify.Notifier
}

func (w *alertWorker) Work(ctx context.Context, job *river.Job[notify.AlertArgs]) error {
	return result(w.n.SendAlert(ctx, job.Args))
}

// AuthCleanupArgs deletes expired login codes, sessions and old email records.
type AuthCleanupArgs struct{}

func (AuthCleanupArgs) Kind() string { return "auth_cleanup" }

// PingArgs reports liveness to an external monitor (Healthchecks.io).
type PingArgs struct{}

func (PingArgs) Kind() string { return "healthcheck_ping" }

// InsertOpts: the next periodic run is the retry.
func (PingArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 1} }

// PingInterval is how often the external monitor hears from us.
const PingInterval = 5 * time.Minute

// Services are what the workers call.
type Services struct {
	Watch  *watch.Service
	Notify *notify.Notifier
	Auth   *auth.Service
	// HealthcheckURL is pinged every PingInterval while the worker runs.
	HealthcheckURL string
}

// NewClient builds the River client with all workers and periodic jobs, and
// hands it to the services as their job queue.
func NewClient(pool *pgxpool.Pool, log *slog.Logger, hb *Heartbeat, s Services) (*river.Client[pgx.Tx], error) {
	svc := s.Watch
	workers := river.NewWorkers()
	river.AddWorker(workers, &heartbeatWorker{hb: hb})
	river.AddWorker(workers, &resolveWorker{svc: svc})
	river.AddWorker(workers, &checkWorker{svc: svc})
	river.AddWorker(workers, &snapshotWorker{svc: svc})
	river.AddWorker(workers, &periodicWorker[watch.ScheduleArgs]{timeout: time.Minute, run: func(ctx context.Context) error {
		n, err := svc.ScheduleDue(ctx)
		if n > 0 {
			log.Debug("checks queued", "count", n)
		}
		return err
	}})
	river.AddWorker(workers, &periodicWorker[watch.ExpireArgs]{timeout: time.Minute, run: svc.Expire})
	river.AddWorker(workers, &periodicWorker[watch.CanaryArgs]{timeout: fetchTimeout, run: func(ctx context.Context) error {
		_, err := svc.Canary(ctx)
		return err
	}})
	river.AddWorker(workers, &periodicWorker[watch.CleanupArgs]{timeout: 10 * time.Minute, run: svc.Cleanup})
	river.AddWorker(workers, &digestWorker{n: s.Notify})
	river.AddWorker(workers, &alertWorker{n: s.Notify})
	river.AddWorker(workers, &periodicWorker[notify.SweepArgs]{timeout: time.Minute, run: func(ctx context.Context) error {
		_, err := s.Notify.Sweep(ctx)
		return err
	}})
	river.AddWorker(workers, &periodicWorker[AuthCleanupArgs]{timeout: time.Minute, run: s.Auth.Cleanup})
	river.AddWorker(workers, &periodicWorker[PingArgs]{timeout: 30 * time.Second, run: func(ctx context.Context) error {
		return ping(ctx, s.HealthcheckURL)
	}})

	periodic := func(every time.Duration, args river.JobArgs, onStart bool) *river.PeriodicJob {
		return river.NewPeriodicJob(river.PeriodicInterval(every),
			func() (river.JobArgs, *river.InsertOpts) { return args, nil },
			&river.PeriodicJobOpts{RunOnStart: onStart})
	}
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger:  log.With("component", "river"),
		Workers: workers,
		Queues: map[string]river.QueueConfig{
			river.QueueDefault:  {MaxWorkers: 2},
			watch.QueueFetch:    {MaxWorkers: 2}, // serialized further by the fetch limiter
			watch.QueueSnapshot: {MaxWorkers: 1}, // one full page in memory at a time
			notify.QueueEmail:   {MaxWorkers: 1}, // serial, so the daily limit count is exact
		},
		PeriodicJobs: []*river.PeriodicJob{
			periodic(HeartbeatInterval, HeartbeatArgs{}, true),
			periodic(watch.ScheduleInterval, watch.ScheduleArgs{}, true),
			periodic(watch.ExpireInterval, watch.ExpireArgs{}, true),
			periodic(watch.CanaryInterval, watch.CanaryArgs{}, false),
			periodic(watch.CleanupInterval, watch.CleanupArgs{}, false),
			periodic(notify.SweepInterval, notify.SweepArgs{}, true),
			periodic(24*time.Hour, AuthCleanupArgs{}, false),
			periodic(PingInterval, PingArgs{}, true),
		},
	})
	if err != nil {
		return nil, err
	}
	svc.SetJobs(client)
	s.Notify.SetJobs(client)
	return client, nil
}

// ping tells the external monitor the worker is alive. A failed ping is
// logged by River and retried at the next interval; the monitor alerts if
// pings stop.
func ping(ctx context.Context, url string) error {
	if url == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("healthcheck ping: HTTP %d", resp.StatusCode)
	}
	return nil
}

// NewInsertOnlyClient is a client that only enqueues jobs, for CLI commands
// that run next to a serving instance.
func NewInsertOnlyClient(pool *pgxpool.Pool, log *slog.Logger) (*river.Client[pgx.Tx], error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{Logger: log.With("component", "river")})
}
