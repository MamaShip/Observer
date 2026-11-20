-- Per-account cap on notification emails per month (plan.md §5.6). NULL
-- means the service default (MONTHLY_EMAIL_LIMIT); a paid plan may raise it.
-- +goose Up
ALTER TABLE users ADD COLUMN monthly_email_limit int CHECK (monthly_email_limit >= 0);
CREATE INDEX email_log_user_sent ON email_log (user_id, sent_at) WHERE user_id IS NOT NULL;

-- +goose Down
DROP INDEX email_log_user_sent;
ALTER TABLE users DROP COLUMN monthly_email_limit;
