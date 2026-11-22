-- Public invitation links (plan.md §6.1).

-- name: CreateInviteLink :one
INSERT INTO invite_links (creator_id, note, max_uses, expires_at)
VALUES (sqlc.arg(creator_id), sqlc.arg(note), sqlc.arg(max_uses), sqlc.arg(expires_at)::timestamptz)
RETURNING *;

-- ListInviteLinks shows each link with its creator and the accounts it
-- created that still exist.
-- name: ListInviteLinks :many
SELECT sqlc.embed(l), coalesce(c.email, '')::text AS creator_email,
  (SELECT count(*) FROM users u WHERE u.invite_link_id = l.id)::int AS accounts
FROM invite_links l LEFT JOIN users c ON c.id = l.creator_id
WHERE (sqlc.narg(only_id)::uuid IS NULL OR l.id = sqlc.narg(only_id)::uuid)
ORDER BY l.created_at DESC
LIMIT 200;

-- name: GetInviteLink :one
SELECT * FROM invite_links WHERE id = $1;

-- LockInviteLink serialises sign-ups through one link so its places hold.
-- name: LockInviteLink :one
SELECT * FROM invite_links WHERE id = $1 FOR UPDATE;

-- ClaimInviteLinkCode counts a login code requested through a link that
-- is still open, up to max_codes.
-- name: ClaimInviteLinkCode :execrows
UPDATE invite_links SET codes_sent = codes_sent + 1
WHERE id = sqlc.arg(id) AND revoked_at IS NULL AND expires_at > sqlc.arg(now)::timestamptz
  AND used < max_uses AND codes_sent < sqlc.arg(max_codes)::int;

-- name: ReleaseInviteLinkCode :exec
UPDATE invite_links SET codes_sent = codes_sent - 1 WHERE id = $1 AND codes_sent > 0;

-- name: UseInviteLink :exec
UPDATE invite_links SET used = used + 1 WHERE id = $1;

-- name: RevokeInviteLink :execrows
UPDATE invite_links SET revoked_at = coalesce(revoked_at, sqlc.arg(now)::timestamptz) WHERE id = sqlc.arg(id);

-- CreateUserFromLink returns no row when the address got an account
-- meanwhile (waiting for that transaction if needed).
-- name: CreateUserFromLink :one
INSERT INTO users (email, invite_link_id) VALUES (sqlc.arg(email), sqlc.arg(invite_link_id))
ON CONFLICT (email) DO NOTHING
RETURNING *;
