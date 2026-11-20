-- Watches that ended and still owe the user an email: every deletion, and
-- expiries for users who asked for them. Expire marks the others notified.
-- name: UsersWithPendingNotifications :many
SELECT DISTINCT w.user_id FROM watches w JOIN users u ON u.id = w.user_id
WHERE w.status = 'ended' AND w.notified_at IS NULL AND w.end_reason IN ('deleted', 'expired')
  AND u.status = 'active';

-- name: PendingNotifications :many
SELECT w.id, w.end_reason, w.ended_at, w.submitted_url, a.title, a.account_name,
       a.deleted_reason_code, a.deleted_reason_text
FROM watches w JOIN articles a ON a.id = w.article_id
WHERE w.user_id = $1 AND w.status = 'ended' AND w.notified_at IS NULL AND w.end_reason IN ('deleted', 'expired')
ORDER BY w.ended_at;

-- name: MarkWatchesNotified :exec
UPDATE watches SET notified_at = sqlc.arg(now)::timestamptz WHERE id = ANY(sqlc.arg(ids)::uuid[]);

-- name: SkipExpiryNotifications :execrows
UPDATE watches w SET notified_at = sqlc.arg(now)::timestamptz
FROM users u
WHERE u.id = w.user_id AND w.end_reason = 'expired' AND w.notified_at IS NULL AND NOT u.notify_on_expire;

-- name: CountEmailsSince :one
SELECT count(*)::bigint FROM email_log WHERE status = 'sent' AND sent_at >= sqlc.arg(since)::timestamptz;

-- name: InsertEmailLog :one
INSERT INTO email_log (user_id, kind, to_email, subject, status)
VALUES (sqlc.narg(user_id), sqlc.arg(kind), sqlc.arg(to_email), sqlc.arg(subject), 'queued')
RETURNING id;

-- name: FinishEmailLog :exec
UPDATE email_log SET status = sqlc.arg(status), provider_id = sqlc.narg(provider_id),
                     last_error = sqlc.narg(last_error), sent_at = sqlc.narg(sent_at)
WHERE id = sqlc.arg(id);
