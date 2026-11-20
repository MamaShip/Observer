package watch

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/MamaShip/Observer/internal/blob"
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
	if err := s.putGzip(ctx, blob.SnapshotKey(snapID.String(), "raw.html.gz"), f.page.Body); err != nil {
		return fmt.Errorf("store raw page: %w", err)
	}
	_, err = s.jobs.Insert(ctx, SnapshotArgs{ArticleID: art.ID, SnapshotID: snapID, URL: url, CapturedAt: f.at}, nil)
	if err == nil {
		s.log.Info("new snapshot queued", "article", art.ID, "snapshot", snapID)
	}
	return err
}
