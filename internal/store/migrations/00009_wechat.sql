-- Official Account entry (plan.md §6.7). A WeChat user (OpenID, which is
-- specific to our account) is bound to at most one user and the other way
-- round. Tokens are one-time links sent in replies: 'bind' starts a binding
-- from WeChat (and may carry an article link to submit once bound), 'login'
-- signs the bound user in on the website. Only hashes are stored.
-- +goose Up
CREATE TABLE wechat_bindings (
  openid     text PRIMARY KEY,
  user_id    uuid NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE wechat_tokens (
  token_hash  bytea PRIMARY KEY,
  purpose     text NOT NULL CHECK (purpose IN ('bind', 'login')),
  openid      text NOT NULL,
  user_id     uuid REFERENCES users (id) ON DELETE CASCADE,
  pending_url text,
  expires_at  timestamptz NOT NULL,
  used_at     timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now(),
  CHECK ((purpose = 'login') = (user_id IS NOT NULL))
);
CREATE INDEX wechat_tokens_expires ON wechat_tokens (expires_at);
CREATE INDEX wechat_tokens_openid ON wechat_tokens (openid);

-- +goose Down
DROP TABLE wechat_tokens;
DROP TABLE wechat_bindings;
