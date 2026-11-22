-- Login codes requested through a public invitation link (plan.md §5.6,
-- §6.1) are counted apart, so one shared link cannot use up the day's
-- email budget that logins and notifications need. A link now opens at
-- most 100 places.
-- +goose Up
ALTER TABLE email_log DROP CONSTRAINT email_log_kind_check;
ALTER TABLE email_log ADD CHECK (kind IN ('login', 'join', 'invite', 'deleted', 'expired', 'alert'));
ALTER TABLE invite_links DROP CONSTRAINT invite_links_max_uses_check;
ALTER TABLE invite_links ADD CHECK (max_uses BETWEEN 1 AND 100);

-- +goose Down
ALTER TABLE invite_links DROP CONSTRAINT invite_links_max_uses_check;
ALTER TABLE invite_links ADD CHECK (max_uses BETWEEN 1 AND 1000);
ALTER TABLE email_log DROP CONSTRAINT email_log_kind_check;
-- Preserve the daily sending count; the previous version counts these as logins.
UPDATE email_log SET kind = 'login' WHERE kind = 'join';
ALTER TABLE email_log ADD CHECK (kind IN ('login', 'invite', 'deleted', 'expired', 'alert'));
