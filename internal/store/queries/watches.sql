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

-- Expiry is never emailed, so the watch is announced as it ends.
-- name: ExpireWatches :many
UPDATE watches SET status = 'ended', end_reason = 'expired', ended_at = sqlc.arg(now)::timestamptz,
                   notified_at = sqlc.arg(now)::timestamptz
WHERE status = 'active' AND expires_at <= sqlc.arg(now)::timestamptz
RETURNING id, user_id, article_id;

-- name: ListUserWatches :many
SELECT w.*, a.title, a.status AS article_status
FROM watches w LEFT JOIN articles a ON a.id = w.article_id
WHERE w.user_id = $1
ORDER BY w.created_at DESC;

-- name: CountOpenWatches :one
SELECT count(*)::bigint FROM watches WHERE user_id = $1 AND status IN ('resolving', 'active');

-- name: CountWatchesSince :one
SELECT count(*)::bigint FROM watches WHERE user_id = sqlc.arg(user_id) AND created_at > sqlc.arg(since)::timestamptz;

-- ListWatchesPage pages newest first by id (UUIDv7 ids sort by creation time).
-- name: ListWatchesPage :many
SELECT w.id, w.article_id, w.submitted_url, w.status, w.end_reason, w.created_at, w.expires_at, w.ended_at,
       a.title, a.account_name, a.status AS article_status, a.deleted_reason_code
FROM watches w LEFT JOIN articles a ON a.id = w.article_id
WHERE w.user_id = sqlc.arg(user_id)
  AND (sqlc.narg(status)::text IS NULL OR w.status = sqlc.narg(status)::text)
  AND (sqlc.narg(before_id)::uuid IS NULL OR w.id < sqlc.narg(before_id)::uuid)
ORDER BY w.id DESC
LIMIT sqlc.arg(max_rows);

-- name: GetUserWatch :one
SELECT sqlc.embed(w), a.title, a.account_name, a.author, a.published_at, a.status AS article_status,
       a.canonical_url, a.deleted_reason_code, a.deleted_reason_text, a.deleted_at,
       a.last_checked_at, a.next_check_at
FROM watches w LEFT JOIN articles a ON a.id = w.article_id
WHERE w.id = sqlc.arg(id) AND w.user_id = sqlc.arg(user_id);

-- name: StopWatch :execrows
UPDATE watches SET status = 'ended', end_reason = 'user_stopped', ended_at = sqlc.arg(now)::timestamptz,
                   notified_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND status IN ('resolving', 'active');

-- name: ListRecentCheckRuns :many
SELECT checked_at, mode, result, reason FROM check_runs
WHERE article_id = sqlc.arg(article_id) ORDER BY checked_at DESC LIMIT sqlc.arg(max_rows);

-- A user may see an article's snapshots if they ever watched it.
-- name: UserCanViewSnapshot :one
SELECT EXISTS (
  SELECT 1 FROM snapshots s JOIN watches w ON w.article_id = s.article_id
  WHERE s.id = sqlc.arg(snapshot_id) AND w.user_id = sqlc.arg(user_id)
);

-- name: LockUserWatch :one
SELECT * FROM watches WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) FOR UPDATE;

-- RenewWatch makes a watch active until expires_at: an active one is
-- extended, an expired or stopped one is watched again.
-- name: RenewWatch :one
UPDATE watches SET status = 'active', expires_at = sqlc.arg(expires_at)::timestamptz,
                   end_reason = NULL, ended_at = NULL, notified_at = NULL
WHERE id = sqlc.arg(id)
RETURNING *;
