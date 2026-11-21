-- Deletions mentioned in an Official Account reply (plan.md §6.7). We cannot
-- push messages, so the next reply to a bound user tells them what was
-- deleted since; the email goes out regardless.
-- +goose Up
ALTER TABLE watches ADD COLUMN wechat_told_at timestamptz;

-- +goose Down
ALTER TABLE watches DROP COLUMN wechat_told_at;
