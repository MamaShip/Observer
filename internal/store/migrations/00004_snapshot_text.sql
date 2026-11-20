-- Plain-text length of a snapshot, so the admin console can flag backups
-- with suspiciously little text. NULL for snapshots made before this column.
-- +goose Up
ALTER TABLE snapshots ADD COLUMN text_runes int;

-- +goose Down
ALTER TABLE snapshots DROP COLUMN text_runes;
