// Package jobs defines the River background jobs and builds the River client.
package jobs

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
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

// Timeout keeps a heartbeat from ever holding a worker slot.
func (w *heartbeatWorker) Timeout(*river.Job[HeartbeatArgs]) time.Duration { return 10 * time.Second }

// NewClient builds the River client with all workers and periodic jobs.
func NewClient(pool *pgxpool.Pool, log *slog.Logger, hb *Heartbeat) (*river.Client[pgx.Tx], error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &heartbeatWorker{hb: hb})

	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger:  log.With("component", "river"),
		Workers: workers,
		Queues: map[string]river.QueueConfig{
			// Fetching is globally serialized by a rate limiter (plan.md §5.5),
			// so a couple of workers is enough.
			river.QueueDefault: {MaxWorkers: 2},
		},
		PeriodicJobs: []*river.PeriodicJob{
			river.NewPeriodicJob(river.PeriodicInterval(HeartbeatInterval),
				func() (river.JobArgs, *river.InsertOpts) { return HeartbeatArgs{}, nil },
				&river.PeriodicJobOpts{RunOnStart: true}),
		},
	})
}
