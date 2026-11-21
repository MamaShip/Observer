-- When an administrator rebuilt a failed capture from its stored page
-- (plan.md §6.3), so the console can tell a rebuilt backup from a fetched one.
-- +goose Up
ALTER TABLE captures ADD COLUMN rebuilt_at timestamptz;

-- +goose Down
ALTER TABLE captures DROP COLUMN rebuilt_at;
