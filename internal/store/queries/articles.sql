-- name: GetArticle :one
SELECT * FROM articles WHERE id = $1;

-- name: GetArticleForUpdate :one
SELECT * FROM articles WHERE id = $1 FOR UPDATE;

-- name: GetArticleByKeyForUpdate :one
SELECT * FROM articles WHERE canonical_key = $1 FOR UPDATE;

-- name: GetArticleByAlias :one
SELECT a.* FROM url_aliases u JOIN articles a ON a.id = u.article_id
WHERE u.url = $1;

-- name: CreateArticle :one
INSERT INTO articles DEFAULT VALUES RETURNING *;

-- CreateAlias reports 0 rows when the URL already belongs to an article
-- (possibly one a concurrent submission created a moment ago).
-- name: CreateAlias :execrows
INSERT INTO url_aliases (url, article_id) VALUES ($1, $2)
ON CONFLICT (url) DO NOTHING;

-- name: MoveAliases :exec
UPDATE url_aliases SET article_id = sqlc.arg(to_id) WHERE article_id = sqlc.arg(from_id);

-- name: MoveCheckRuns :exec
UPDATE check_runs SET article_id = sqlc.arg(to_id) WHERE article_id = sqlc.arg(from_id);

-- name: DeleteArticle :exec
DELETE FROM articles WHERE id = $1;

-- name: MarkArticleAlive :one
UPDATE articles SET
  status                = 'alive',
  canonical_key         = sqlc.arg(canonical_key)::text,
  canonical_url         = sqlc.arg(canonical_url)::text,
  title                 = sqlc.arg(title)::text,
  account_name          = sqlc.arg(account_name)::text,
  author                = sqlc.arg(author)::text,
  published_at          = sqlc.narg(published_at),
  deleted_reason_code   = NULL,
  deleted_reason_text   = NULL,
  deleted_at            = NULL,
  deletion_suspected_at = NULL,
  consecutive_failures  = 0,
  last_checked_at       = sqlc.arg(now)::timestamptz,
  last_alive_at         = sqlc.arg(now)::timestamptz,
  next_check_at         = sqlc.arg(next_check_at)::timestamptz
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: MarkArticleInvalid :exec
UPDATE articles SET
  status              = 'invalid',
  deleted_reason_code = sqlc.narg(reason_code),
  deleted_reason_text = sqlc.narg(reason_text),
  last_checked_at     = sqlc.arg(now)::timestamptz,
  next_check_at       = NULL
WHERE id = sqlc.arg(id);

-- name: RecordArticleAlive :exec
UPDATE articles SET
  last_checked_at       = sqlc.arg(now)::timestamptz,
  last_alive_at         = sqlc.arg(now)::timestamptz,
  consecutive_failures  = 0,
  deletion_suspected_at = NULL,
  next_check_at         = sqlc.arg(next_check_at)::timestamptz
WHERE id = sqlc.arg(id);

-- name: SuspectArticleDeleted :exec
UPDATE articles SET
  last_checked_at       = sqlc.arg(now)::timestamptz,
  consecutive_failures  = 0,
  deletion_suspected_at = sqlc.arg(now)::timestamptz,
  next_check_at         = sqlc.arg(next_check_at)::timestamptz
WHERE id = sqlc.arg(id);

-- PostponeConfirmation keeps the first deleted answer and looks again once
-- the confirmation delay after it is over.
-- name: PostponeConfirmation :exec
UPDATE articles SET
  last_checked_at      = sqlc.arg(now)::timestamptz,
  consecutive_failures = 0,
  next_check_at        = sqlc.arg(next_check_at)::timestamptz
WHERE id = sqlc.arg(id);

-- name: ConfirmArticleDeleted :exec
UPDATE articles SET
  status              = 'deleted',
  deleted_reason_code = sqlc.arg(reason_code)::text,
  deleted_reason_text = sqlc.arg(reason_text)::text,
  deleted_at          = sqlc.arg(now)::timestamptz,
  last_checked_at     = sqlc.arg(now)::timestamptz,
  next_check_at       = NULL
WHERE id = sqlc.arg(id);

-- name: RecordArticleFailure :one
UPDATE articles SET
  last_checked_at      = sqlc.arg(now)::timestamptz,
  consecutive_failures = consecutive_failures + 1,
  next_check_at        = sqlc.arg(next_check_at)::timestamptz
WHERE id = sqlc.arg(id)
RETURNING consecutive_failures;

-- ReactivateArticle forgets a deleted answer from before the article went
-- dormant: the article may have been online in between.
-- name: ReactivateArticle :exec
UPDATE articles SET status = 'alive', deletion_suspected_at = NULL, next_check_at = sqlc.arg(next_check_at)::timestamptz
WHERE id = sqlc.arg(id) AND status = 'dormant';

-- name: RequestCheckNow :execrows
UPDATE articles SET next_check_at = now() WHERE id = $1 AND status = 'alive';

-- name: DueArticles :many
SELECT id FROM articles
WHERE status = 'alive' AND next_check_at <= sqlc.arg(now)::timestamptz
ORDER BY next_check_at
LIMIT sqlc.arg(max_rows);

-- name: MarkUnwatchedArticlesDormant :execrows
UPDATE articles a SET status = 'dormant', next_check_at = NULL
WHERE a.status = 'alive'
  AND NOT EXISTS (SELECT 1 FROM watches w WHERE w.article_id = a.id AND w.status IN ('active', 'resolving'));

-- name: ListArticles :many
SELECT a.*,
  (SELECT count(*) FROM watches w WHERE w.article_id = a.id AND w.status = 'active')::bigint AS active_watches,
  (SELECT count(*) FROM snapshots s WHERE s.article_id = a.id)::bigint AS snapshot_count
FROM articles a
ORDER BY a.first_seen_at DESC
LIMIT sqlc.arg(max_rows);

-- name: InsertCheckRun :exec
INSERT INTO check_runs (article_id, checked_at, mode, result, reason, rule, http_status,
                        wire_bytes, duration_ms, detail, state_change)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: DeleteOldCheckRuns :execrows
DELETE FROM check_runs WHERE checked_at < sqlc.arg(before)::timestamptz AND NOT state_change;
