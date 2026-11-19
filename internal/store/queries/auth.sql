-- name: CountRecentLoginTokens :one
SELECT count(*)::bigint FROM login_tokens
WHERE email = sqlc.arg(email) AND created_at > sqlc.arg(since)::timestamptz;

-- name: CreateLoginToken :exec
INSERT INTO login_tokens (email, token_hash, expires_at, request_ip)
VALUES (sqlc.arg(email), sqlc.arg(token_hash), sqlc.arg(expires_at), sqlc.narg(request_ip));

-- ConsumeLoginToken marks a token used, at most once, if it has not expired.
-- name: ConsumeLoginToken :one
UPDATE login_tokens SET used_at = sqlc.arg(now)::timestamptz
WHERE token_hash = sqlc.arg(token_hash) AND used_at IS NULL AND expires_at > sqlc.arg(now)::timestamptz
RETURNING email;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: CreateUser :one
INSERT INTO users (email) VALUES ($1)
ON CONFLICT (email) DO UPDATE SET email = users.email
RETURNING *;

-- name: TouchUserLogin :exec
UPDATE users SET last_login_at = sqlc.arg(now)::timestamptz WHERE id = sqlc.arg(id);

-- name: SetUserStatus :execrows
UPDATE users SET status = sqlc.arg(status) WHERE email = sqlc.arg(email);

-- name: UpdateUserSettings :one
UPDATE users SET notify_on_expire = sqlc.arg(notify_on_expire) WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;

-- name: CreateSession :exec
INSERT INTO sessions (user_id, token_hash, user_agent, expires_at)
VALUES (sqlc.arg(user_id), sqlc.arg(token_hash), sqlc.narg(user_agent), sqlc.arg(expires_at));

-- name: GetSession :one
SELECT sqlc.embed(s), sqlc.embed(u)
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = sqlc.arg(token_hash) AND s.expires_at > sqlc.arg(now)::timestamptz;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = sqlc.arg(now)::timestamptz, expires_at = sqlc.arg(expires_at)::timestamptz
WHERE id = sqlc.arg(id);

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;

-- name: DeleteUserSessions :execrows
DELETE FROM sessions WHERE user_id = $1;

-- name: DeleteExpiredLoginTokens :execrows
DELETE FROM login_tokens WHERE expires_at < sqlc.arg(before)::timestamptz;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at < sqlc.arg(now)::timestamptz;
