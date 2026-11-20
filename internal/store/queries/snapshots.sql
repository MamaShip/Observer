-- name: GetSnapshot :one
SELECT * FROM snapshots WHERE id = $1;

-- name: ArticleHasSnapshot :one
SELECT EXISTS (SELECT 1 FROM snapshots WHERE article_id = $1);

-- name: InsertSnapshot :exec
INSERT INTO snapshots (id, article_id, captured_at, content_hash, title, html_key, raw_key,
                       size_bytes, asset_count, missing_assets, text_runes)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: ListSnapshots :many
SELECT * FROM snapshots WHERE article_id = $1 ORDER BY captured_at DESC;

-- name: GetAsset :one
SELECT * FROM assets WHERE sha256 = $1;

-- name: InsertAsset :exec
INSERT INTO assets (sha256, content_type, size_bytes) VALUES ($1, $2, $3)
ON CONFLICT (sha256) DO NOTHING;

-- name: InsertSnapshotAsset :exec
INSERT INTO snapshot_assets (snapshot_id, sha256, source_url) VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;
