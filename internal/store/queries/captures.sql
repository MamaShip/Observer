-- name: CreateCapture :exec
INSERT INTO captures (id, article_id, raw_key, url, captured_at)
VALUES (sqlc.arg(id), sqlc.arg(article_id), sqlc.arg(raw_key), sqlc.arg(url), sqlc.arg(captured_at));

-- name: GetCapture :one
SELECT * FROM captures WHERE id = $1;

-- name: GetCaptureForUpdate :one
SELECT * FROM captures WHERE id = $1 FOR UPDATE;

-- name: FinishCapture :exec
UPDATE captures SET status = 'done', error_code = NULL, error = NULL, updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id);

-- name: FailCapture :exec
UPDATE captures SET status = 'failed', error_code = sqlc.arg(error_code), error = sqlc.arg(error),
                    updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id) AND status <> 'done';

-- name: RetryCapture :exec
UPDATE captures SET status = 'pending', error_code = NULL, error = NULL, updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id);

-- name: ListCaptures :many
SELECT * FROM captures WHERE article_id = $1 ORDER BY captured_at DESC, id DESC LIMIT 50;

-- BackupState is the newest capture's state, and how many snapshots an
-- article has.
-- name: BackupState :one
SELECT (SELECT count(*) FROM snapshots s WHERE s.article_id = sqlc.arg(article_id))::int AS snapshots,
       coalesce((SELECT c.status FROM captures c WHERE c.article_id = sqlc.arg(article_id)
                 ORDER BY c.captured_at DESC, c.id DESC LIMIT 1), '')::text AS latest_status,
       coalesce((SELECT c.error_code FROM captures c WHERE c.article_id = sqlc.arg(article_id)
                 ORDER BY c.captured_at DESC, c.id DESC LIMIT 1), '')::text AS latest_error;
