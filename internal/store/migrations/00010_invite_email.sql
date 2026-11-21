-- +goose Up
ALTER TABLE invites
  ADD COLUMN mail_queued boolean NOT NULL DEFAULT false,
  ADD COLUMN token_hash bytea UNIQUE,
  ADD COLUMN token_expires_at timestamptz,
  ADD COLUMN mail_sent_at timestamptz;

ALTER TABLE email_log DROP CONSTRAINT email_log_kind_check;
ALTER TABLE email_log ADD CHECK (kind IN ('login', 'invite', 'deleted', 'expired', 'alert'));

-- +goose Down
DELETE FROM river_job WHERE kind = 'email_invite';
ALTER TABLE email_log DROP CONSTRAINT email_log_kind_check;
-- Preserve the daily sending count; the previous version groups these with logins.
UPDATE email_log SET kind = 'login' WHERE kind = 'invite';
ALTER TABLE email_log ADD CHECK (kind IN ('login', 'deleted', 'expired', 'alert'));
ALTER TABLE invites DROP COLUMN mail_sent_at, DROP COLUMN token_expires_at,
  DROP COLUMN token_hash, DROP COLUMN mail_queued;
