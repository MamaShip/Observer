-- name: UpsertUserByEmail :one
INSERT INTO users (email) VALUES ($1)
ON CONFLICT (email) DO UPDATE SET email = users.email
RETURNING *;

-- name: CreateWatch :one
INSERT INTO watches (user_id, article_id, submitted_url, status, end_reason, expires_at, ended_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetOpenWatch :one
SELECT * FROM watches
WHERE user_id = sqlc.arg(user_id) AND article_id = sqlc.arg(article_id)::uuid AND status IN ('resolving', 'active')
ORDER BY created_at
LIMIT 1;

-- MoveWatches is used when a pending article turns out to be a known one.
-- A pending article never has active watches, so the one-active-watch
-- index cannot conflict.
-- name: MoveWatches :exec
UPDATE watches SET article_id = sqlc.arg(to_id)::uuid WHERE article_id = sqlc.arg(from_id)::uuid;

-- ActivateResolvingWatches turns at most one resolving watch per user into
-- an active one (a user may have submitted two different links of the same
-- article); DeleteResolvingWatches then removes the leftovers.
-- name: ActivateResolvingWatches :execrows
UPDATE watches w SET status = 'active'
WHERE w.id IN (
  SELECT DISTINCT ON (r.user_id) r.id FROM watches r
  WHERE r.article_id = sqlc.arg(article_id)::uuid AND r.status = 'resolving'
    AND NOT EXISTS (SELECT 1 FROM watches a
                    WHERE a.user_id = r.user_id AND a.article_id = r.article_id AND a.status = 'active')
  ORDER BY r.user_id, r.created_at
);

-- name: DeleteResolvingWatches :execrows
DELETE FROM watches WHERE article_id = sqlc.arg(article_id)::uuid AND status = 'resolving';

-- name: FailResolvingWatches :execrows
UPDATE watches SET status = 'failed', end_reason = sqlc.arg(end_reason)::text, ended_at = sqlc.arg(now)::timestamptz
WHERE article_id = sqlc.arg(article_id)::uuid AND status = 'resolving';

-- name: EndActiveWatchesDeleted :many
UPDATE watches SET status = 'ended', end_reason = 'deleted', ended_at = sqlc.arg(now)::timestamptz
WHERE article_id = sqlc.arg(article_id)::uuid AND status = 'active'
RETURNING id, user_id;

-- name: ExpireWatches :many
UPDATE watches SET status = 'ended', end_reason = 'expired', ended_at = sqlc.arg(now)::timestamptz
WHERE status = 'active' AND expires_at <= sqlc.arg(now)::timestamptz
RETURNING id, user_id, article_id;

-- name: ListUserWatches :many
SELECT w.*, a.title, a.status AS article_status
FROM watches w LEFT JOIN articles a ON a.id = w.article_id
WHERE w.user_id = $1
ORDER BY w.created_at DESC;
