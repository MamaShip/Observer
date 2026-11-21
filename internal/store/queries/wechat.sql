-- name: CreateWechatToken :exec
INSERT INTO wechat_tokens (token_hash, purpose, openid, user_id, pending_url, expires_at)
VALUES (sqlc.arg(token_hash), sqlc.arg(purpose), sqlc.arg(openid), sqlc.narg(user_id), sqlc.narg(pending_url), sqlc.arg(expires_at));

-- UseWechatToken marks an unused, unexpired token as used and returns it.
-- No row means it is unknown, used, expired or for another purpose.
-- name: UseWechatToken :one
UPDATE wechat_tokens SET used_at = sqlc.arg(now)::timestamptz
WHERE token_hash = sqlc.arg(token_hash) AND purpose = sqlc.arg(purpose)
  AND used_at IS NULL AND expires_at > sqlc.arg(now)::timestamptz
RETURNING *;

-- LockWechatOpenID serialises bindings of one OpenID (there may be no row
-- to lock yet).
-- name: LockWechatOpenID :exec
SELECT pg_advisory_xact_lock(hashtextextended('wechat:' || sqlc.arg(openid)::text, 0));

-- name: DeleteWechatBindingsFor :exec
DELETE FROM wechat_bindings WHERE openid = sqlc.arg(openid) OR user_id = sqlc.arg(user_id);

-- name: CreateWechatBinding :one
INSERT INTO wechat_bindings (openid, user_id) VALUES (sqlc.arg(openid), sqlc.arg(user_id)) RETURNING *;

-- DeleteWechatLoginTokens removes login links that no longer match a binding.
-- name: DeleteWechatLoginTokens :exec
DELETE FROM wechat_tokens WHERE purpose = 'login' AND (openid = sqlc.arg(openid) OR user_id = sqlc.arg(user_id));

-- name: GetWechatBindingByOpenID :one
SELECT sqlc.embed(b), sqlc.embed(u)
FROM wechat_bindings b JOIN users u ON u.id = b.user_id
WHERE b.openid = $1;

-- name: GetWechatBindingByUser :one
SELECT * FROM wechat_bindings WHERE user_id = $1;

-- name: GetWechatBindingForUpdate :one
SELECT * FROM wechat_bindings WHERE openid = sqlc.arg(openid) AND user_id = sqlc.arg(user_id) FOR UPDATE;

-- name: DeleteWechatBindingByUser :one
DELETE FROM wechat_bindings WHERE user_id = $1 RETURNING openid;

-- name: DeleteWechatBindingByOpenID :execrows
DELETE FROM wechat_bindings WHERE openid = $1;

-- name: DeleteWechatTokensByOpenID :exec
DELETE FROM wechat_tokens WHERE openid = $1;

-- name: DeleteExpiredWechatTokens :execrows
DELETE FROM wechat_tokens WHERE expires_at < sqlc.arg(now)::timestamptz;

-- name: CountWechatBindings :one
SELECT count(*)::int FROM wechat_bindings;

-- A confirmation ends active watches and records its check at the same
-- instant. A submission of an already deleted article has no such check.
-- name: ListWechatNews :many
SELECT w.id, w.ended_at, a.title
FROM watches w JOIN articles a ON a.id = w.article_id
WHERE w.user_id = sqlc.arg(user_id) AND w.end_reason = 'deleted' AND w.wechat_told_at IS NULL
  AND w.ended_at > sqlc.arg(since)::timestamptz
  AND EXISTS (SELECT 1 FROM check_runs c WHERE c.article_id = w.article_id
              AND c.checked_at = w.ended_at AND c.result = 'deleted' AND c.state_change)
ORDER BY w.ended_at, w.id
LIMIT sqlc.arg(max_rows);

-- Only the reply that first claims a notice includes it.
-- name: MarkWechatNews :many
UPDATE watches SET wechat_told_at = sqlc.arg(now)::timestamptz
WHERE user_id = sqlc.arg(user_id) AND id = ANY(sqlc.arg(ids)::uuid[])
  AND end_reason = 'deleted' AND wechat_told_at IS NULL
RETURNING id;
