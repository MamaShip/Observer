-- name: Stats :one
SELECT
  (SELECT count(*) FROM users)::bigint                                  AS users,
  (SELECT count(*) FROM watches WHERE status = 'active')::bigint        AS active_watches,
  (SELECT count(*) FROM articles)::bigint                               AS articles,
  (SELECT count(*) FROM articles WHERE status = 'deleted')::bigint      AS deleted_articles,
  (SELECT count(*) FROM snapshots)::bigint                              AS snapshots,
  (SELECT count(*) FROM check_runs
    WHERE checked_at > now() - interval '24 hours')::bigint             AS checks_24h,
  (SELECT count(*) FROM check_runs
    WHERE checked_at > now() - interval '24 hours'
      AND result = 'blocked')::bigint                                   AS blocked_24h;
