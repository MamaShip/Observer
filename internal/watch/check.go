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
// check at least deletionConfirmDelay after the first agrees, however early
// the second check runs. Everything else is recorded and the article is
// retried with backoff. The state is re-read under a row lock after the
// fetch, so a concurrent change is not overwritten.
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
		art, err := q.GetArticleForUpdate(ctx, art.ID)
		if err != nil {
			return err
		}
		if art.Status != "alive" {
			log.Info("article changed during the check; recording the answer only", "status", art.Status)
			return s.recordCheck(ctx, q, art.ID, f, false)
		}
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
			if confirmAt := art.DeletionSuspectedAt.Add(deletionConfirmDelay); f.at.Before(confirmAt) {
				// Checked early (an admin asked for it): this answer does not
				// confirm the first one, and the confirmation stays due.
				log.Info("article still looks deleted; too early to confirm", "confirm_at", confirmAt)
				if err := q.PostponeConfirmation(ctx, db.PostponeConfirmationParams{
					ID: art.ID, Now: f.at, NextCheckAt: confirmAt,
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
