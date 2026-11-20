package watch

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MamaShip/Observer/internal/store/db"
)

// Renewal (plan.md §6.5): a watch can be given another WatchDays from now.
// An active watch is extended; one that ended because its period ran out or
// the user stopped it is watched again, as long as the article is online.

// Errors from Renew.
var (
	ErrNotRenewable = errors.New("this watch cannot be renewed")
	// ErrWatchingAgain: the user already watches the article in another
	// watch (they submitted it again after this one ended).
	ErrWatchingAgain = errors.New("the article is already watched in another watch")
)

// Renewable reports whether a watch in this state can be renewed.
func Renewable(status, endReason, articleStatus string) bool {
	switch status {
	case "active":
		return true
	case "ended":
		return (endReason == "expired" || endReason == "user_stopped") &&
			(articleStatus == "alive" || articleStatus == "dormant")
	}
	return false
}

// Renew sets the user's watch to end WatchDays from now and makes it active.
// An article nobody watched any more is checked again at once. Reopening an
// ended watch counts toward quota.MaxOpen (ErrOpenLimit); the day's
// submissions are not affected. It returns pgx.ErrNoRows if the watch is not
// the user's.
func (s *Service) Renew(ctx context.Context, userID, watchID uuid.UUID, quota Quota) (db.Watch, error) {
	var w db.Watch
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		// Serialised with the user's submissions, which count open watches too.
		if _, err := q.LockUser(ctx, userID); err != nil {
			return err
		}
		cur, err := q.LockUserWatch(ctx, db.LockUserWatchParams{ID: watchID, UserID: userID})
		if err != nil {
			return err
		}
		if cur.ArticleID == nil {
			return ErrNotRenewable
		}
		now := s.now()
		if cur.Status != "active" {
			art, err := q.GetArticleForUpdate(ctx, *cur.ArticleID)
			if err != nil {
				return err
			}
			if !Renewable(cur.Status, deref(cur.EndReason), art.Status) {
				return ErrNotRenewable
			}
			if _, err := q.GetOpenWatch(ctx, db.GetOpenWatchParams{UserID: userID, ArticleID: art.ID}); err == nil {
				return ErrWatchingAgain
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err := checkOpen(ctx, q, userID, quota); err != nil {
				return err
			}
			if art.Status == "dormant" {
				if err := q.ReactivateArticle(ctx, db.ReactivateArticleParams{ID: art.ID, NextCheckAt: now}); err != nil {
					return err
				}
			}
		}
		w, err = q.RenewWatch(ctx, db.RenewWatchParams{ID: cur.ID, ExpiresAt: now.AddDate(0, 0, s.cfg.WatchDays)})
		return err
	})
	if err == nil {
		s.log.Info("watch renewed", "watch", w.ID, "until", w.ExpiresAt)
	}
	return w, err
}
