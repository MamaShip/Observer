-- Initial schema (plan.md §7). Until the first production deploy this file may
-- still be edited in place; after that, every change needs a new migration.

-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
  id               uuid PRIMARY KEY DEFAULT uuidv7(),
  email            citext NOT NULL UNIQUE,
  role             text NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
  status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
  notify_on_expire boolean NOT NULL DEFAULT false,
  created_at       timestamptz NOT NULL DEFAULT now(),
  last_login_at    timestamptz
);

CREATE TABLE login_tokens (
  id         uuid PRIMARY KEY DEFAULT uuidv7(),
  email      citext NOT NULL,
  token_hash bytea NOT NULL UNIQUE,
  expires_at timestamptz NOT NULL,
  used_at    timestamptz,
  request_ip inet,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX login_tokens_expires ON login_tokens (expires_at);

CREATE TABLE sessions (
  id           uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  token_hash   bytea NOT NULL UNIQUE,
  user_agent   text,
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);

CREATE TABLE articles (
  id                   uuid PRIMARY KEY DEFAULT uuidv7(),
  canonical_key        text UNIQUE, -- biz:mid:idx; NULL until the first successful fetch
  canonical_url        text,        -- msg_link from the page; used for every later check
  status               text NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending', 'alive', 'deleted', 'invalid', 'dormant')),
  title                text,
  account_name         text,
  author               text,
  published_at         timestamptz,
  deleted_reason_code  text CHECK (deleted_reason_code IN
                         ('author_deleted', 'violation', 'complaint', 'account_blocked', 'unknown')),
  deleted_reason_text  text,
  deleted_at           timestamptz,
  first_seen_at        timestamptz NOT NULL DEFAULT now(),
  last_checked_at      timestamptz,
  last_alive_at        timestamptz,
  next_check_at        timestamptz,
  consecutive_failures int NOT NULL DEFAULT 0
);
CREATE INDEX articles_next_check ON articles (next_check_at)
  WHERE status IN ('pending', 'alive');

CREATE TABLE url_aliases (
  url        text PRIMARY KEY, -- pre-normalized URL (wechat.Link.Key)
  article_id uuid NOT NULL REFERENCES articles (id),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX url_aliases_article ON url_aliases (article_id);

CREATE TABLE watches (
  id            uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id       uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  article_id    uuid REFERENCES articles (id), -- may be NULL while resolving
  submitted_url text NOT NULL,
  status        text NOT NULL CHECK (status IN ('resolving', 'active', 'ended', 'failed')),
  end_reason    text CHECK (end_reason IN ('deleted', 'expired', 'user_stopped', 'invalid')),
  created_at    timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,
  ended_at      timestamptz,
  notified_at   timestamptz
);
CREATE UNIQUE INDEX watches_one_active ON watches (user_id, article_id)
  WHERE status = 'active';
CREATE INDEX watches_user ON watches (user_id, created_at DESC);
CREATE INDEX watches_article_active ON watches (article_id) WHERE status = 'active';

CREATE TABLE snapshots (
  id             uuid PRIMARY KEY DEFAULT uuidv7(),
  article_id     uuid NOT NULL REFERENCES articles (id),
  captured_at    timestamptz NOT NULL DEFAULT now(),
  content_hash   bytea NOT NULL, -- sha256 of the plain text, for future change detection
  title          text,
  html_key       text NOT NULL,  -- Wasabi object keys
  raw_key        text NOT NULL,
  size_bytes     bigint NOT NULL,
  asset_count    int NOT NULL,
  missing_assets int NOT NULL DEFAULT 0
);
CREATE INDEX snapshots_article ON snapshots (article_id, captured_at DESC);

CREATE TABLE assets (
  sha256       bytea PRIMARY KEY,
  content_type text NOT NULL,
  size_bytes   bigint NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE snapshot_assets (
  snapshot_id uuid NOT NULL REFERENCES snapshots (id) ON DELETE CASCADE,
  sha256      bytea NOT NULL REFERENCES assets (sha256),
  source_url  text NOT NULL,
  PRIMARY KEY (snapshot_id, sha256)
);

-- One row per check. result is the wechat.Verdict; reason and rule explain it.
CREATE TABLE check_runs (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  article_id  uuid NOT NULL REFERENCES articles (id),
  checked_at  timestamptz NOT NULL DEFAULT now(),
  mode        text NOT NULL CHECK (mode IN ('full', 'check', 'head')),
  result      text NOT NULL CHECK (result IN ('alive', 'deleted', 'invalid', 'blocked', 'error')),
  reason      text,
  rule        text,
  http_status int,
  wire_bytes  int,
  duration_ms int,
  detail      text
);
CREATE INDEX check_runs_article ON check_runs (article_id, checked_at DESC);
CREATE INDEX check_runs_checked ON check_runs (checked_at);

CREATE TABLE email_log (
  id          uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id     uuid REFERENCES users (id) ON DELETE SET NULL,
  kind        text NOT NULL CHECK (kind IN ('login', 'deleted', 'expired', 'alert')),
  to_email    citext NOT NULL,
  subject     text NOT NULL,
  status      text NOT NULL CHECK (status IN ('queued', 'sent', 'failed')),
  provider_id text,
  last_error  text,
  created_at  timestamptz NOT NULL DEFAULT now(),
  sent_at     timestamptz
);
CREATE INDEX email_log_created ON email_log (created_at);

-- +goose Down
DROP TABLE email_log;
DROP TABLE check_runs;
DROP TABLE snapshot_assets;
DROP TABLE assets;
DROP TABLE snapshots;
DROP TABLE watches;
DROP TABLE url_aliases;
DROP TABLE articles;
DROP TABLE sessions;
DROP TABLE login_tokens;
DROP TABLE users;
