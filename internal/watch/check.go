package watch

import (
	"context"
	"errors"
	"fmt"
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
			// The notification sweep emails these users (notify.Sweep).
			log.Info("article deleted", "reason", r.Reason, "watches_ended", len(ended))

		default: // invalid, blocked, error: no conclusion
			n := art.ConsecutiveFailures + 1
			if _, err := q.RecordArticleFailure(ctx, db.RecordArticleFailureParams{
				ID: art.ID, Now: f.at, NextCheckAt: f.at.Add(jitter(failureBackoff(n))),
			}); err != nil {
				return err
			}
			if n == alertAfterFailures {
				s.alert.Alert(ctx, "failing:"+art.ID.String(), "文章连续检查失败",
					fmt.Sprintf("文章 %s（%s）已连续 %d 次检查没有结论，最近一次：%s / %s %s",
						art.ID, deref(art.Title), n, r.Verdict, r.Rule, strings.Join(nonBlank(r.Message, r.Detail), " | ")))
			}
		}
		return s.recordCheck(ctx, q, art.ID, f, stateChange)
	})
}
