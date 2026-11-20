-- The end of a watch period is never emailed; only deletions are
-- (plan.md §5.6, decided 2026-11-20). Expired watches count as announced.
-- +goose Up
UPDATE watches SET notified_at = coalesce(ended_at, now())
WHERE end_reason = 'expired' AND notified_at IS NULL;
ALTER TABLE users DROP COLUMN notify_on_expire;

-- +goose Down
ALTER TABLE users ADD COLUMN notify_on_expire boolean NOT NULL DEFAULT false;
