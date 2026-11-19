package watch

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/MamaShip/Observer/internal/store/db"
	"github.com/MamaShip/Observer/internal/wechat"
)

// Check looks at an alive article once (plan.md §5.3, §5.5). Only alive and
// deleted answers change anything; a deletion counts only when a second
// check about ten minutes later agrees. Everything else is recorded and the
// article is retried with backoff.
func (s *Service) Check(ctx context.Context, args CheckArgs) error {
	art, err := s.q.GetArticle(ctx, args.ArticleID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if art.Status != "alive" || art.CanonicalUrl == nil {
		return nil
	}
	if err := s.snoozeIfOpen(); err != nil {
		return err
	}

	f, err := s.fetchPage(ctx, *art.CanonicalUrl, wechat.FetchCheck)
	if err != nil {
		return err
	}
	r := f.result
	log := s.log.With("article", art.ID, "verdict", r.Verdict, "rule", r.Rule)

	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		stateChange := false
		switch r.Verdict {
		case wechat.VerdictAlive:
			if err := q.RecordArticleAlive(ctx, db.RecordArticleAliveParams{
				ID: art.ID, Now: f.at, NextCheckAt: f.at.Add(jitter(checkInterval(f.at.Sub(art.FirstSeenAt)))),
			}); err != nil {
				return err
			}

		case wechat.VerdictDeleted:
			if art.DeletionSuspectedAt == nil {
				log.Info("article looks deleted; confirming in 10 minutes")
				if err := q.SuspectArticleDeleted(ctx, db.SuspectArticleDeletedParams{
					ID: art.ID, Now: f.at, NextCheckAt: f.at.Add(deletionConfirmDelay),
				}); err != nil {
					return err
				}
				break
			}
			stateChange = true
			if err := q.ConfirmArticleDeleted(ctx, db.ConfirmArticleDeletedParams{
				ID: art.ID, Now: f.at, ReasonCode: string(r.Reason),
				ReasonText: strings.Join(nonBlank(r.Message, r.Detail), " | "),
			}); err != nil {
				return err
			}
			ended, err := q.EndActiveWatchesDeleted(ctx, db.EndActiveWatchesDeletedParams{ArticleID: art.ID, Now: f.at})
			if err != nil {
				return err
			}
			// M3 queues the deletion emails here, in this transaction.
			log.Info("article deleted", "reason", r.Reason, "watches_ended", len(ended))

		default: // invalid, blocked, error: no conclusion
			n := art.ConsecutiveFailures + 1
			if _, err := q.RecordArticleFailure(ctx, db.RecordArticleFailureParams{
				ID: art.ID, Now: f.at, NextCheckAt: f.at.Add(jitter(failureBackoff(n))),
			}); err != nil {
				return err
			}
			if n >= alertAfterFailures {
				log.Warn("article checks keep failing", "consecutive_failures", n)
			}
		}
		return s.recordCheck(ctx, q, art.ID, f, stateChange)
	})
}
