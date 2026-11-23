-- Watches that ended because the article was deleted and still owe the
-- user an email. (Only deletions are emailed; Expire marks expiries done.)
-- name: UsersWithPendingNotifications :many
SELECT DISTINCT w.user_id FROM watches w JOIN users u ON u.id = w.user_id
WHERE w.status = 'ended' AND w.notified_at IS NULL AND w.end_reason = 'deleted'
  AND u.status = 'active';

-- name: PendingNotifications :many
SELECT w.id, w.end_reason, w.ended_at, w.submitted_url, a.title, a.account_name,
       a.deleted_reason_code, a.deleted_reason_text
FROM watches w JOIN articles a ON a.id = w.article_id
WHERE w.user_id = $1 AND w.status = 'ended' AND w.notified_at IS NULL AND w.end_reason = 'deleted'
ORDER BY w.ended_at;

-- name: MarkWatchesNotified :exec
UPDATE watches SET notified_at = sqlc.arg(now)::timestamptz WHERE id = ANY(sqlc.arg(ids)::uuid[]);


-- name: CountEmailsSince :one
SELECT count(*)::bigint FROM email_log WHERE status = 'sent' AND sent_at >= sqlc.arg(since)::timestamptz;

-- LockEmailBudget serialises claims on the day's sending budget, so two
-- messages cannot both take its last place.
-- name: LockEmailBudget :exec
SELECT pg_advisory_xact_lock(hashtextextended('email-budget', 0));

-- CountEmailBudget counts the day's email against the sending limit: sent,
-- and being sent (queued, sent_at not yet set). Failed messages give their
-- place back.
-- name: CountEmailBudget :one
SELECT count(*)::bigint AS total, count(*) FILTER (WHERE kind = 'join')::bigint AS joins
FROM email_log
WHERE (status = 'sent' AND sent_at >= sqlc.arg(since)::timestamptz)
   OR (status = 'queued' AND created_at >= sqlc.arg(since)::timestamptz);

-- name: InsertEmailLog :one
INSERT INTO email_log (user_id, kind, to_email, subject, status)
VALUES (sqlc.narg(user_id), sqlc.arg(kind), sqlc.arg(to_email), sqlc.arg(subject), 'queued')
RETURNING id;

-- name: FinishEmailLog :exec
UPDATE email_log SET status = sqlc.arg(status), provider_id = sqlc.narg(provider_id),
                     last_error = sqlc.narg(last_error), sent_at = sqlc.narg(sent_at)
WHERE id = sqlc.arg(id);

-- CountUserNotificationsSince counts the notification emails (not login
-- codes) sent to a user since a time.
-- name: CountUserNotificationsSince :one
SELECT count(*)::int FROM email_log
WHERE user_id = sqlc.arg(user_id) AND kind IN ('deleted', 'expired') AND status = 'sent'
  AND sent_at >= sqlc.arg(since)::timestamptz;
