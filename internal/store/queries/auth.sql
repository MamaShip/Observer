-- ClaimLoginRequest counts one code for (email, day). It returns no row,
-- counting nothing, when the day's limit is reached or the last code was
-- requested after resend_before.
-- name: ClaimLoginRequest :one
INSERT INTO login_requests AS r (email, day, sent, last_at)
VALUES (sqlc.arg(email), sqlc.arg(day)::date, 1, sqlc.arg(now)::timestamptz)
ON CONFLICT (email, day) DO UPDATE SET sent = r.sent + 1, last_at = excluded.last_at
WHERE r.sent < sqlc.arg(per_day)::int AND r.last_at <= sqlc.arg(resend_before)::timestamptz
RETURNING sent;

-- name: GetLoginRequest :one
SELECT * FROM login_requests WHERE email = sqlc.arg(email) AND day = sqlc.arg(day)::date;

-- ReleaseLoginRequest gives back a claim whose email could not be sent.
-- name: ReleaseLoginRequest :exec
UPDATE login_requests SET sent = greatest(sent - 1, 0)
WHERE email = sqlc.arg(email) AND day = sqlc.arg(day)::date;

-- name: CreateLoginCode :exec
INSERT INTO login_codes (email, code_hash, expires_at, request_ip)
VALUES (sqlc.arg(email), sqlc.arg(code_hash), sqlc.arg(expires_at), sqlc.narg(request_ip));

-- AttemptLoginCode counts one guess against the address's latest code and
-- returns it, unless that code expired, was used or has no guesses left.
-- name: AttemptLoginCode :one
UPDATE login_codes SET attempts = attempts + 1
WHERE id = (
  SELECT c.id FROM login_codes c
  WHERE c.email = sqlc.arg(email) AND c.used_at IS NULL AND c.expires_at > sqlc.arg(now)::timestamptz
  ORDER BY c.created_at DESC LIMIT 1
) AND attempts < sqlc.arg(max_attempts)::int
RETURNING id, code_hash;

-- name: ConsumeLoginCode :execrows
UPDATE login_codes SET used_at = sqlc.arg(now)::timestamptz WHERE id = sqlc.arg(id) AND used_at IS NULL;

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

-- Sessions and watches go with the user (ON DELETE CASCADE).
-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;

-- name: DeleteLoginCodesFor :exec
DELETE FROM login_codes WHERE email = $1;

-- ScrubEmailLog keeps the rows (they count toward today's sending limit) but
-- forgets the address.
-- name: ScrubEmailLog :exec
UPDATE email_log SET to_email = 'deleted', user_id = NULL
WHERE user_id = sqlc.arg(user_id) OR to_email = sqlc.arg(email);

-- name: DeleteOldEmailLog :execrows
DELETE FROM email_log WHERE created_at < sqlc.arg(before)::timestamptz;

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

-- name: DeleteExpiredLoginCodes :execrows
DELETE FROM login_codes WHERE expires_at < sqlc.arg(before)::timestamptz;

-- name: DeleteOldLoginRequests :execrows
DELETE FROM login_requests WHERE day < sqlc.arg(before)::date;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at < sqlc.arg(now)::timestamptz;

-- name: SetMonthlyEmailLimit :execrows
UPDATE users SET monthly_email_limit = sqlc.narg(monthly_email_limit) WHERE email = sqlc.arg(email);
