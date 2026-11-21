-- Queries for the admin console (plan.md §10.1). Months and days are counted
-- in Beijing time; the caller passes the boundaries.

-- name: AdminTotals :one
SELECT
  (SELECT count(*) FROM users)::int                                              AS users,
  (SELECT count(*) FROM users WHERE status = 'disabled')::int                    AS disabled_users,
  (SELECT count(*) FROM users WHERE last_login_at > now() - interval '30 days')::int AS active_users_30d,
  (SELECT count(*) FROM watches WHERE status IN ('resolving', 'active'))::int    AS open_watches,
  (SELECT count(*) FROM watches)::int                                            AS watches,
  (SELECT count(*) FROM articles)::int                                           AS articles,
  (SELECT count(*) FROM articles WHERE status = 'alive')::int                    AS articles_alive,
  (SELECT count(*) FROM articles WHERE status = 'deleted')::int                  AS articles_deleted,
  (SELECT count(*) FROM articles WHERE status = 'invalid')::int                  AS articles_invalid,
  (SELECT count(*) FROM articles WHERE status = 'dormant')::int                  AS articles_dormant,
  (SELECT count(*) FROM articles WHERE status = 'pending')::int                  AS articles_pending,
  (SELECT count(*) FROM snapshots)::int                                          AS snapshots,
  (SELECT coalesce(sum(size_bytes), 0) FROM snapshots)::bigint                   AS snapshot_bytes,
  (SELECT count(*) FROM assets)::int                                             AS assets,
  (SELECT coalesce(sum(size_bytes), 0) FROM assets)::bigint                      AS asset_bytes;

-- name: AdminUsage :one
SELECT
  (SELECT count(*) FROM email_log WHERE status = 'sent' AND sent_at >= sqlc.arg(since)::timestamptz)::int AS emails,
  (SELECT count(*) FROM email_log WHERE status = 'sent' AND kind = 'login' AND sent_at >= sqlc.arg(since)::timestamptz)::int AS login_emails,
  (SELECT count(*) FROM email_log WHERE status = 'sent' AND kind = 'invite' AND sent_at >= sqlc.arg(since)::timestamptz)::int AS invite_emails,
  (SELECT count(*) FROM email_log WHERE status = 'sent' AND kind IN ('deleted', 'expired') AND sent_at >= sqlc.arg(since)::timestamptz)::int AS notification_emails,
  (SELECT count(*) FROM email_log WHERE status = 'sent' AND kind = 'alert' AND sent_at >= sqlc.arg(since)::timestamptz)::int AS alert_emails,
  (SELECT count(*) FROM email_log WHERE status = 'failed' AND created_at >= sqlc.arg(since)::timestamptz)::int AS failed_emails,
  (SELECT count(*) FROM check_runs WHERE checked_at >= sqlc.arg(since)::timestamptz)::int AS checks,
  (SELECT count(*) FROM check_runs WHERE checked_at >= sqlc.arg(since)::timestamptz AND result = 'blocked')::int AS blocked,
  (SELECT count(*) FROM check_runs WHERE checked_at >= sqlc.arg(since)::timestamptz AND result = 'error')::int AS errors,
  (SELECT coalesce(sum(wire_bytes), 0) FROM check_runs WHERE checked_at >= sqlc.arg(since)::timestamptz)::bigint AS page_bytes,
  (SELECT coalesce(sum(size_bytes), 0) FROM assets WHERE created_at >= sqlc.arg(since)::timestamptz)::bigint AS new_asset_bytes,
  (SELECT count(*) FROM users WHERE created_at >= sqlc.arg(since)::timestamptz)::int AS new_users,
  (SELECT count(*) FROM watches WHERE created_at >= sqlc.arg(since)::timestamptz)::int AS new_watches,
  (SELECT count(*) FROM articles WHERE first_seen_at >= sqlc.arg(since)::timestamptz)::int AS new_articles,
  (SELECT count(*) FROM articles WHERE deleted_at >= sqlc.arg(since)::timestamptz)::int AS deletions,
  (SELECT count(*) FROM snapshots WHERE captured_at >= sqlc.arg(since)::timestamptz)::int AS new_snapshots;

-- AdminDailyCounts is one row per metric and Beijing day that has any.
-- name: AdminDailyCounts :many
SELECT t.metric::text AS metric, t.day::date AS day, t.n::int AS n FROM (
  SELECT 'watches' AS metric, ((created_at AT TIME ZONE 'UTC') + interval '8 hours')::date AS day, count(*) AS n
    FROM watches WHERE created_at >= sqlc.arg(since)::timestamptz GROUP BY 2
  UNION ALL
  SELECT 'users', ((created_at AT TIME ZONE 'UTC') + interval '8 hours')::date, count(*)
    FROM users WHERE created_at >= sqlc.arg(since)::timestamptz GROUP BY 2
  UNION ALL
  SELECT 'deletions', ((deleted_at AT TIME ZONE 'UTC') + interval '8 hours')::date, count(*)
    FROM articles WHERE deleted_at >= sqlc.arg(since)::timestamptz GROUP BY 2
  UNION ALL
  SELECT 'checks', ((checked_at AT TIME ZONE 'UTC') + interval '8 hours')::date, count(*)
    FROM check_runs WHERE checked_at >= sqlc.arg(since)::timestamptz GROUP BY 2
  UNION ALL
  SELECT 'blocked', ((checked_at AT TIME ZONE 'UTC') + interval '8 hours')::date, count(*)
    FROM check_runs WHERE checked_at >= sqlc.arg(since)::timestamptz AND result = 'blocked' GROUP BY 2
  UNION ALL
  SELECT 'emails', ((sent_at AT TIME ZONE 'UTC') + interval '8 hours')::date, count(*)
    FROM email_log WHERE status = 'sent' AND sent_at >= sqlc.arg(since)::timestamptz GROUP BY 2
  UNION ALL
  SELECT 'snapshots', ((captured_at AT TIME ZONE 'UTC') + interval '8 hours')::date, count(*)
    FROM snapshots WHERE captured_at >= sqlc.arg(since)::timestamptz GROUP BY 2
) t
ORDER BY 2, 1;

-- name: AdminListUsers :many
SELECT u.id, u.email, u.role, u.status, u.created_at, u.last_login_at, u.monthly_email_limit,
  (SELECT count(*) FROM watches w WHERE w.user_id = u.id AND w.status IN ('resolving', 'active'))::int AS open_watches,
  (SELECT count(*) FROM watches w WHERE w.user_id = u.id)::int AS total_watches,
  (SELECT count(*) FROM watches w WHERE w.user_id = u.id AND w.created_at >= sqlc.arg(month_start)::timestamptz)::int AS watches_this_month,
  (SELECT count(*) FROM email_log e WHERE e.user_id = u.id AND e.kind IN ('deleted', 'expired') AND e.status = 'sent'
     AND e.sent_at >= sqlc.arg(month_start)::timestamptz)::int AS emails_this_month,
  (i.id IS NOT NULL)::bool AS invited,
  i.inviter_id AS inviter_id,
  coalesce(inv.email, '')::text AS inviter_email,
  (SELECT count(*) FROM invites x WHERE x.inviter_id = u.id AND x.invitee_id IS NOT NULL)::int AS invitees,
  (SELECT count(*) FROM invites x WHERE x.inviter_id = u.id AND x.created_at >= sqlc.arg(month_start)::timestamptz)::int AS invites_this_month,
  wb.created_at AS wechat_bound_at
FROM users u
LEFT JOIN invites i ON i.invitee_id = u.id
LEFT JOIN users inv ON inv.id = i.inviter_id
LEFT JOIN wechat_bindings wb ON wb.user_id = u.id
WHERE (sqlc.narg(before)::uuid IS NULL OR u.id < sqlc.narg(before)::uuid)
  AND (sqlc.narg(only_id)::uuid IS NULL OR u.id = sqlc.narg(only_id)::uuid)
  AND (sqlc.narg(invited_by)::uuid IS NULL OR i.inviter_id = sqlc.narg(invited_by)::uuid)
  AND (sqlc.narg(search)::text IS NULL OR u.email ILIKE '%' || sqlc.narg(search)::text || '%')
ORDER BY u.id DESC
LIMIT sqlc.arg(max_rows)::int;

-- name: SetUserRole :execrows
UPDATE users SET role = sqlc.arg(role) WHERE email = sqlc.arg(email);

-- AdminListArticles lists articles with their latest snapshot. With
-- issues_only, only those whose backup looks incomplete (keep in step with
-- api.articleIssues): no snapshot although the page was fetched, images that
-- could not be stored, under 100 characters of text, or repeated failed checks.
-- issue keeps only one kind of problem, by the same rules.
-- name: AdminListArticles :many
SELECT a.id, a.status, a.title, a.account_name, a.first_seen_at, a.last_checked_at, a.deleted_at,
  a.deleted_reason_code, a.consecutive_failures,
  (SELECT count(*) FROM watches w WHERE w.article_id = a.id)::int AS watchers,
  (SELECT count(*) FROM snapshots s WHERE s.article_id = a.id)::int AS snapshots,
  -- sqlc cannot tell these may be NULL after the LEFT JOIN: coalesce them,
  -- and has_snapshot says whether they mean anything.
  (ls.id IS NOT NULL)::bool AS has_snapshot,
  coalesce(ls.id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS snapshot_id,
  coalesce(ls.captured_at, a.first_seen_at)::timestamptz AS captured_at,
  coalesce(ls.asset_count, 0)::int AS asset_count,
  coalesce(ls.missing_assets, 0)::int AS missing_assets,
  ls.text_runes,
  coalesce(lc.status, '')::text AS capture_status,
  -- Rows matching the filters from the cursor on: all of them on the first page.
  (count(*) OVER ())::int AS matched
FROM articles a
LEFT JOIN (
  SELECT DISTINCT ON (s.article_id) s.article_id, s.id, s.captured_at, s.asset_count, s.missing_assets, s.text_runes
  FROM snapshots s ORDER BY s.article_id, s.captured_at DESC
) ls ON ls.article_id = a.id
LEFT JOIN (
  SELECT DISTINCT ON (c.article_id) c.article_id, c.status
  FROM captures c ORDER BY c.article_id, c.captured_at DESC, c.id DESC
) lc ON lc.article_id = a.id
WHERE (sqlc.narg(status)::text IS NULL OR a.status = sqlc.narg(status)::text)
  AND (NOT sqlc.arg(issues_only)::bool OR
       (ls.id IS NULL AND a.status IN ('alive', 'deleted', 'dormant'))
       OR ls.missing_assets > 0
       OR ls.text_runes < 100
       OR a.consecutive_failures >= 3
       OR lc.status = 'failed')
  AND (sqlc.narg(issue)::text IS NULL
       OR (sqlc.narg(issue)::text = 'backup_failed' AND lc.status = 'failed')
       OR (sqlc.narg(issue)::text = 'no_snapshot' AND ls.id IS NULL AND lc.status IS DISTINCT FROM 'failed'
           AND a.status IN ('alive', 'deleted', 'dormant'))
       OR (sqlc.narg(issue)::text = 'missing_images' AND ls.missing_assets > 0)
       OR (sqlc.narg(issue)::text = 'short_text' AND ls.text_runes < 100)
       OR (sqlc.narg(issue)::text = 'failing_checks' AND a.consecutive_failures >= 3))
  AND (sqlc.narg(search)::text IS NULL OR a.title ILIKE '%' || sqlc.narg(search)::text || '%'
       OR a.account_name ILIKE '%' || sqlc.narg(search)::text || '%')
  AND (sqlc.narg(before)::uuid IS NULL OR a.id < sqlc.narg(before)::uuid)
ORDER BY a.id DESC
LIMIT sqlc.arg(max_rows)::int;

-- name: AdminCountArticles :one
SELECT count(*)::int FROM articles;

-- name: AdminArticleWatchers :many
SELECT w.id, w.status, w.end_reason, w.created_at, w.ended_at, u.email
FROM watches w JOIN users u ON u.id = w.user_id
WHERE w.article_id = $1
ORDER BY w.created_at;

-- name: AdminCheckRuns :many
SELECT checked_at, mode, result, reason, rule, http_status, wire_bytes, duration_ms
FROM check_runs WHERE article_id = sqlc.arg(article_id)
ORDER BY checked_at DESC LIMIT sqlc.arg(max_rows)::int;
