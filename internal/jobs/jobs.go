// Package jobs runs the background work on River: thin workers that call
// the watch service, the periodic jobs, and the client that ties them up.
package jobs

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

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
	switch {
	case err == nil:
		return nil
	case errors.As(err, &snooze):
		return river.JobSnooze(snooze.After)
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

// NewClient builds the River client with all workers and periodic jobs, and
// hands it to the service as its job queue.
func NewClient(pool *pgxpool.Pool, log *slog.Logger, hb *Heartbeat, svc *watch.Service) (*river.Client[pgx.Tx], error) {
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
		},
		PeriodicJobs: []*river.PeriodicJob{
			periodic(HeartbeatInterval, HeartbeatArgs{}, true),
			periodic(watch.ScheduleInterval, watch.ScheduleArgs{}, true),
			periodic(watch.ExpireInterval, watch.ExpireArgs{}, true),
			periodic(watch.CanaryInterval, watch.CanaryArgs{}, false),
			periodic(watch.CleanupInterval, watch.CleanupArgs{}, false),
		},
	})
	if err != nil {
		return nil, err
	}
	svc.SetJobs(client)
	return client, nil
}

// NewInsertOnlyClient is a client that only enqueues jobs, for CLI commands
// that run next to a serving instance.
func NewInsertOnlyClient(pool *pgxpool.Pool, log *slog.Logger) (*river.Client[pgx.Tx], error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{Logger: log.With("component", "river")})
}
