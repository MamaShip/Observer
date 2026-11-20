package watch

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/MamaShip/Observer/internal/blob"
	"github.com/MamaShip/Observer/internal/store/db"
	"github.com/MamaShip/Observer/internal/wechat"
)

// ResnapshotArgs fetches an article that is still online again and makes a
// new snapshot of it, for a backup that came out incomplete (admin console).
// Earlier snapshots stay.
type ResnapshotArgs struct {
	ArticleID uuid.UUID `json:"article_id" river:"unique"`
}

func (ResnapshotArgs) Kind() string { return "resnapshot" }

func (ResnapshotArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueFetch, Priority: 3, MaxAttempts: 5,
		UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: openStates}}
}

// ErrNotOnline means the article cannot be fetched again: it is deleted,
// invalid, or was never fetched.
var ErrNotOnline = errors.New("article is not online")

// QueueResnapshot schedules a new snapshot of an article that is online.
func (s *Service) QueueResnapshot(ctx context.Context, articleID uuid.UUID) error {
	art, err := s.q.GetArticle(ctx, articleID)
	if err != nil {
		return err
	}
	if (art.Status != "alive" && art.Status != "dormant") || art.CanonicalUrl == nil {
		return ErrNotOnline
	}
	_, err = s.jobs.Insert(ctx, ResnapshotArgs{ArticleID: articleID}, nil)
	return err
}

// Resnapshot runs a ResnapshotArgs job: fetch the full page, store it, and
// queue the snapshot job as Resolve does. If WeChat no longer serves the
// article, the check is recorded and the job ends; the regular checks
// handle deletions.
func (s *Service) Resnapshot(ctx context.Context, args ResnapshotArgs) error {
	art, err := s.q.GetArticle(ctx, args.ArticleID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if art.CanonicalUrl == nil {
		return permanent(ErrNotOnline)
	}
	if err := s.snoozeIfOpen(); err != nil {
		return err
	}
	url := *art.CanonicalUrl
	f, err := s.fetchPage(ctx, url, wechat.FetchFull)
	if err != nil {
		return err
	}
	if err := s.recordCheck(ctx, s.q, art.ID, f, false); err != nil {
		return err
	}
	if f.result.Verdict != wechat.VerdictAlive {
		return permanent(fmt.Errorf("%w: WeChat answered %s (%s)", ErrNotOnline, f.result.Verdict, f.result.Rule))
	}
	snapID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	rawKey := blob.SnapshotKey(snapID.String(), "raw.html.gz")
	if err := s.putGzip(ctx, rawKey, f.page.Body); err != nil {
		return fmt.Errorf("store raw page: %w", err)
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return s.queueSnapshot(ctx, tx, SnapshotArgs{ArticleID: art.ID, SnapshotID: snapID, URL: url, CapturedAt: f.at}, rawKey)
	})
	if err == nil {
		s.log.Info("new snapshot queued", "article", art.ID, "snapshot", snapID)
	}
	return err
}

// ErrCaptureDone means the capture already has its snapshot.
var ErrCaptureDone = errors.New("this capture already has its snapshot")

// RebuildCapture queues the build of a capture's snapshot again, from the
// raw page stored when it was fetched. WeChat is not contacted, so this works
// after the article was deleted; use it once the cause of the failure is
// fixed. It returns pgx.ErrNoRows for an unknown capture.
func (s *Service) RebuildCapture(ctx context.Context, captureID uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		c, err := q.GetCaptureForUpdate(ctx, captureID)
		if err != nil {
			return err
		}
		if c.Status == "done" {
			return ErrCaptureDone
		}
		if err := q.RetryCapture(ctx, db.RetryCaptureParams{ID: c.ID, Now: s.now()}); err != nil {
			return err
		}
		_, err = s.jobs.InsertTx(ctx, tx, SnapshotArgs{ArticleID: c.ArticleID, SnapshotID: c.ID, URL: c.Url, CapturedAt: c.CapturedAt}, nil)
		if err == nil {
			s.log.Info("snapshot rebuild queued", "article", c.ArticleID, "capture", c.ID)
		}
		return err
	})
}
