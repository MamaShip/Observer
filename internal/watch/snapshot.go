package watch

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/MamaShip/Observer/internal/blob"
	"github.com/MamaShip/Observer/internal/snapshot"
	"github.com/MamaShip/Observer/internal/store/db"
)

// queueSnapshot records a stored raw page as a pending capture and queues
// the job that builds its snapshot, in the caller's transaction.
func (s *Service) queueSnapshot(ctx context.Context, tx pgx.Tx, args SnapshotArgs, rawKey string) error {
	if err := s.q.WithTx(tx).CreateCapture(ctx, db.CreateCaptureParams{
		ID: args.SnapshotID, ArticleID: args.ArticleID, RawKey: rawKey, Url: args.URL, CapturedAt: args.CapturedAt,
	}); err != nil {
		return err
	}
	_, err := s.jobs.InsertTx(ctx, tx, args, nil)
	return err
}

// Snapshot builds the reading page for an article from the raw page stored
// by Resolve, storing its images (docs/design.md §6.3). It is idempotent. When the
// page cannot be backed up, or final is set and this attempt fails too, the
// capture is marked failed, so nobody waits for a backup that will not come.
func (s *Service) Snapshot(ctx context.Context, args SnapshotArgs, final bool) error {
	err := s.snapshot(ctx, args)
	if err == nil || (!final && !errors.Is(err, ErrPermanent)) {
		return err
	}
	code := "error"
	if errors.Is(err, snapshot.ErrNoContent) {
		code = "no_content"
	}
	// The job's context may be what ran out; record the failure regardless.
	mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if ferr := s.q.FailCapture(mctx, db.FailCaptureParams{ID: args.SnapshotID, ErrorCode: &code, Error: ptr(err.Error()), Now: s.now()}); ferr != nil {
		s.log.Error("could not record failed capture", "snapshot", args.SnapshotID, "err", ferr)
	}
	s.log.Warn("snapshot failed", "article", args.ArticleID, "snapshot", args.SnapshotID, "reason", code, "err", err)
	return err
}

func (s *Service) snapshot(ctx context.Context, args SnapshotArgs) error {
	if _, err := s.q.GetSnapshot(ctx, args.SnapshotID); err == nil {
		return s.q.FinishCapture(ctx, db.FinishCaptureParams{ID: args.SnapshotID, Now: s.now()})
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	art, err := s.q.GetArticle(ctx, args.ArticleID)
	if err != nil {
		return err
	}
	// Jobs queued before captures were recorded derive the key.
	rawKey := blob.SnapshotKey(args.SnapshotID.String(), "raw.html.gz")
	if c, err := s.q.GetCapture(ctx, args.SnapshotID); err == nil {
		rawKey = c.RawKey
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	raw, err := s.getGzip(ctx, rawKey)
	if err != nil {
		return fmt.Errorf("read raw page: %w", err)
	}

	sess := s.newSession()
	defer sess.Close()
	src := snapshot.Source{
		URL: args.URL, Title: deref(art.Title), AccountName: deref(art.AccountName), Author: deref(art.Author),
		CapturedAt: args.CapturedAt,
	}
	if art.PublishedAt != nil {
		src.PublishedAt = *art.PublishedAt
	}
	res, err := snapshot.Build(ctx, raw, src, func(ctx context.Context, url string) (snapshot.AssetRef, error) {
		return s.storeAsset(ctx, sess, url, args.URL)
	})
	if errors.Is(err, snapshot.ErrNoContent) {
		return permanent(err)
	}
	if err != nil {
		return err
	}

	htmlKey := blob.SnapshotKey(args.SnapshotID.String(), "page.html")
	if err := s.blob.Put(ctx, htmlKey, bytes.NewReader(res.HTML), int64(len(res.HTML)), "text/html; charset=utf-8"); err != nil {
		return fmt.Errorf("store page: %w", err)
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.InsertSnapshot(ctx, db.InsertSnapshotParams{
			ID: args.SnapshotID, ArticleID: art.ID, CapturedAt: args.CapturedAt,
			ContentHash: res.ContentHash[:], Title: art.Title, HtmlKey: htmlKey, RawKey: rawKey,
			SizeBytes: int64(len(res.HTML)), AssetCount: int32(len(res.Assets)), MissingAssets: int32(res.Missing),
			TextRunes: ptr(int32(res.TextRunes)),
		}); err != nil {
			return err
		}
		for _, a := range res.Assets {
			if err := q.InsertSnapshotAsset(ctx, db.InsertSnapshotAssetParams{
				SnapshotID: args.SnapshotID, Sha256: a.SHA256[:], SourceUrl: a.SourceURL,
			}); err != nil {
				return err
			}
		}
		return q.FinishCapture(ctx, db.FinishCaptureParams{ID: args.SnapshotID, Now: s.now()})
	})
	if err == nil {
		s.log.Info("snapshot stored", "article", art.ID, "snapshot", args.SnapshotID,
			"bytes", len(res.HTML), "images", len(res.Assets), "missing", res.Missing)
	}
	return err
}

// storeAsset downloads one image and stores it under its sha256, unless an
// identical image is already stored.
func (s *Service) storeAsset(ctx context.Context, sess Session, url, referer string) (snapshot.AssetRef, error) {
	a, err := sess.FetchAsset(ctx, url, referer)
	if err != nil {
		return snapshot.AssetRef{}, err
	}
	if a.Status != http.StatusOK || a.Size == 0 {
		return snapshot.AssetRef{}, fmt.Errorf("image %s: HTTP %d, %d bytes", url, a.Status, a.Size)
	}
	ctype := imageType(a.ContentType, a.Body)
	if ctype == "" {
		return snapshot.AssetRef{}, fmt.Errorf("image %s: not an image (%s)", url, a.ContentType)
	}
	ref := snapshot.AssetRef{SHA256: sha256.Sum256(a.Body), ContentType: ctype}

	if _, err := s.q.GetAsset(ctx, ref.SHA256[:]); err == nil {
		return ref, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ref, err
	}
	if err := s.blob.Put(ctx, blob.AssetKey(ref.SHA256), bytes.NewReader(a.Body), a.Size, ctype); err != nil {
		return ref, err
	}
	return ref, s.q.InsertAsset(ctx, db.InsertAssetParams{Sha256: ref.SHA256[:], ContentType: ctype, SizeBytes: a.Size})
}

// imageType returns a safe image content type, trusting the bytes over the
// header, or "" if the body is not an image.
func imageType(header string, body []byte) string {
	sniffed := http.DetectContentType(body)
	if strings.HasPrefix(sniffed, "image/") {
		return sniffed
	}
	// DetectContentType does not know SVG or AVIF; accept them from the header.
	if mt, _, err := mime.ParseMediaType(header); err == nil && (mt == "image/svg+xml" || mt == "image/avif") {
		return mt
	}
	return ""
}

func (s *Service) getGzip(ctx context.Context, key string) ([]byte, error) {
	r, err := s.blob.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zr)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
