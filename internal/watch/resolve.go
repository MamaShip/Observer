package watch

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MamaShip/Observer/internal/blob"
	"github.com/MamaShip/Observer/internal/store/db"
	"github.com/MamaShip/Observer/internal/wechat"
)

// Resolve fetches a pending article for the first time (docs/design.md §6.2–6.3).
// final is true on the last attempt: if WeChat still gives no answer, the
// waiting watches fail as unreachable instead of being retried.
func (s *Service) Resolve(ctx context.Context, args ResolveArgs, final bool) error {
	art, err := s.q.GetArticle(ctx, args.ArticleID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // merged into another article meanwhile
	}
	if err != nil {
		return err
	}
	if art.Status != "pending" {
		return nil
	}
	if err := s.snoozeIfOpen(); err != nil {
		return err
	}

	f, err := s.fetchPage(ctx, args.URL, wechat.FetchFull)
	if err != nil {
		return err
	}
	r := f.result
	log := s.log.With("article", art.ID, "url", args.URL, "verdict", r.Verdict, "rule", r.Rule)

	switch {
	case r.Verdict == wechat.VerdictAlive && r.Meta != nil && r.Meta.CanonicalKey() != "":
		return s.resolveAlive(ctx, art, args, f)

	case r.Verdict == wechat.VerdictDeleted || r.Verdict == wechat.VerdictInvalid:
		log.Info("submitted article is not available")
		return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			q := s.q.WithTx(tx)
			p := db.MarkArticleInvalidParams{ID: art.ID, Now: f.at, ReasonText: nonEmpty(strings.Join(nonBlank(r.Message, r.Detail), " | "))}
			if r.Reason != "" {
				p.ReasonCode = ptr(string(r.Reason))
			}
			if err := q.MarkArticleInvalid(ctx, p); err != nil {
				return err
			}
			if _, err := q.FailResolvingWatches(ctx, db.FailResolvingWatchesParams{ArticleID: art.ID, EndReason: "invalid", Now: f.at}); err != nil {
				return err
			}
			return s.recordCheck(ctx, q, art.ID, f, true)
		})

	default: // blocked, error, or an "alive" page we could not identify
		if err := s.recordCheck(ctx, s.q, art.ID, f, false); err != nil {
			return err
		}
		if !final {
			log.Info("first fetch inconclusive, will retry")
			return fmt.Errorf("first fetch inconclusive: %s (%s)", r.Verdict, r.Rule)
		}
		log.Warn("first fetch failed on every attempt; giving up")
		_, err := s.q.FailResolvingWatches(ctx, db.FailResolvingWatchesParams{ArticleID: art.ID, EndReason: "unreachable", Now: f.at})
		return err
	}
}

func (s *Service) resolveAlive(ctx context.Context, pending db.Article, args ResolveArgs, f fetched) error {
	meta := f.result.Meta
	key := meta.CanonicalKey()

	// Later checks use WeChat's own share link; it may carry the real chksm.
	checkURL := args.URL
	var linkKey string
	if l, err := wechat.ParseLink(strings.TrimSuffix(meta.MsgLink, "#rd")); err == nil {
		checkURL, linkKey = l.ServiceFetchURL(), l.Key
	}

	// Store the raw page first; the snapshot job reads it back. Done outside
	// the transaction so a slow upload does not hold row locks.
	snapID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	rawKey := blob.SnapshotKey(snapID.String(), "raw.html.gz")
	if err := s.putGzip(ctx, rawKey, f.page.Body); err != nil {
		return fmt.Errorf("store raw page: %w", err)
	}

	needSnapshot := false
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.GetArticleForUpdate(ctx, pending.ID); err != nil {
			return err
		}
		target := pending
		existing, err := q.GetArticleByKeyForUpdate(ctx, &key)
		switch {
		case err == nil && existing.ID != pending.ID:
			// Another link of a known article: fold this one into it.
			if err := q.MoveAliases(ctx, db.MoveAliasesParams{FromID: pending.ID, ToID: existing.ID}); err != nil {
				return err
			}
			if err := q.MoveWatches(ctx, db.MoveWatchesParams{FromID: pending.ID, ToID: existing.ID}); err != nil {
				return err
			}
			if err := q.MoveCheckRuns(ctx, db.MoveCheckRunsParams{FromID: pending.ID, ToID: existing.ID}); err != nil {
				return err
			}
			if err := q.DeleteArticle(ctx, pending.ID); err != nil {
				return err
			}
			target = existing
			s.log.Info("submitted link is a known article", "article", existing.ID, "merged", pending.ID)
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return err
		}

		p := db.MarkArticleAliveParams{
			ID: target.ID, CanonicalKey: key, CanonicalUrl: checkURL,
			Title: meta.Title, AccountName: meta.AccountName, Author: meta.Author,
			Now: f.at, NextCheckAt: f.at.Add(jitter(checkInterval(f.at.Sub(target.FirstSeenAt)))),
		}
		if !meta.PublishedAt.IsZero() {
			p.PublishedAt = &meta.PublishedAt
		}
		if _, err := q.MarkArticleAlive(ctx, p); err != nil {
			return err
		}
		if linkKey != "" {
			if _, err := q.CreateAlias(ctx, db.CreateAliasParams{Url: linkKey, ArticleID: target.ID}); err != nil {
				return err
			}
		}
		if _, err := q.ActivateResolvingWatches(ctx, target.ID); err != nil {
			return err
		}
		if _, err := q.DeleteResolvingWatches(ctx, target.ID); err != nil { // duplicates of an activated watch
			return err
		}
		if err := s.recordCheck(ctx, q, target.ID, f, true); err != nil {
			return err
		}

		has, err := q.ArticleHasSnapshot(ctx, target.ID)
		if err != nil || has {
			return err
		}
		needSnapshot = true
		return s.queueSnapshot(ctx, tx, SnapshotArgs{
			ArticleID: target.ID, SnapshotID: snapID, URL: checkURL, CapturedAt: f.at,
		}, rawKey)
	})
	if err != nil || needSnapshot {
		return err
	}
	// The article was already backed up; the raw copy is not needed.
	if err := s.blob.Delete(ctx, rawKey); err != nil {
		s.log.Warn("could not delete unneeded raw page", "key", rawKey, "err", err)
	}
	return nil
}

func (s *Service) putGzip(ctx context.Context, key string, data []byte) error {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return s.blob.Put(ctx, key, &buf, int64(buf.Len()), "application/gzip")
}
