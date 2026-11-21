package watch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// Queues. Page fetches are serialized by the Limiter anyway; snapshots hold
// a whole page in memory, so they run one at a time.
const (
	QueueFetch    = "fetch"
	QueueSnapshot = "snapshot"
)

// JobInserter is the part of the River client the service uses.
type JobInserter interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// openStates makes a job unique while one is queued or running, but lets a
// new one be inserted once the previous one finished.
var openStates = []rivertype.JobState{
	rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable,
	rivertype.JobStateRunning, rivertype.JobStateScheduled,
}

// ResolveArgs fetches a newly submitted article for the first time.
type ResolveArgs struct {
	ArticleID uuid.UUID `json:"article_id" river:"unique"`
	URL       string    `json:"url"`
}

// ResolveMaxAttempts spreads retries over about four hours with River's
// default backoff (attempt^4 seconds).
const ResolveMaxAttempts = 10

func (ResolveArgs) Kind() string { return "resolve" }

func (ResolveArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueFetch, Priority: 1, MaxAttempts: ResolveMaxAttempts,
		UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: openStates}}
}

// CheckArgs checks whether an article is still online.
type CheckArgs struct {
	ArticleID uuid.UUID `json:"article_id"`
}

func (CheckArgs) Kind() string { return "check" }

func (CheckArgs) InsertOpts() river.InsertOpts {
	// Inconclusive checks are recorded and rescheduled by the service;
	// attempts only matter if the job itself crashes.
	return river.InsertOpts{Queue: QueueFetch, Priority: 2, MaxAttempts: 3,
		UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: openStates}}
}

// SnapshotArgs builds a snapshot from a stored raw page.
type SnapshotArgs struct {
	ArticleID  uuid.UUID `json:"article_id"`
	SnapshotID uuid.UUID `json:"snapshot_id" river:"unique"`
	URL        string    `json:"url"` // article link, sent as Referer for images
	CapturedAt time.Time `json:"captured_at"`
	// Rebuild is set (Unix nanoseconds) when an administrator rebuilds the
	// capture. It keeps the rebuild from being taken for a duplicate of the
	// earlier job, which river may not have marked finished yet.
	Rebuild int64 `json:"rebuild,omitempty" river:"unique"`
}

func (SnapshotArgs) Kind() string { return "snapshot" }

func (SnapshotArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueSnapshot, MaxAttempts: 6,
		UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: openStates}}
}

// Periodic jobs.
type (
	ScheduleArgs struct{} // enqueue checks for due articles
	ExpireArgs   struct{} // end watches past their period
	CanaryArgs   struct{} // verify detection against known pages
	CleanupArgs  struct{} // prune old check runs
)

func (ScheduleArgs) Kind() string { return "schedule_checks" }
func (ExpireArgs) Kind() string   { return "expire_watches" }
func (CanaryArgs) Kind() string   { return "canary" }
func (CleanupArgs) Kind() string  { return "cleanup" }

// SnoozeError asks the job runner to retry after a delay without using up an
// attempt (the circuit breaker is open).
type SnoozeError struct{ After time.Duration }

func (e *SnoozeError) Error() string { return fmt.Sprintf("snooze for %s", e.After) }

// ErrPermanent marks failures that retrying cannot fix.
var ErrPermanent = errors.New("permanent failure")

func permanent(err error) error { return fmt.Errorf("%w: %w", ErrPermanent, err) }
