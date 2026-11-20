-- LockUser serialises one user's invitations so the monthly count holds.
-- name: LockUser :one
SELECT * FROM users WHERE id = $1 FOR UPDATE;

-- name: CountInvitesSince :one
SELECT count(*)::int FROM invites
WHERE inviter_id = sqlc.arg(inviter_id) AND created_at >= sqlc.arg(since)::timestamptz;

-- name: GetInviteByInviter :one
SELECT * FROM invites WHERE inviter_id = sqlc.arg(inviter_id) AND email = sqlc.arg(email)
ORDER BY created_at DESC LIMIT 1;

-- name: CreateInvite :one
INSERT INTO invites (inviter_id, email, invitee_id)
VALUES (sqlc.narg(inviter_id), sqlc.arg(email), sqlc.narg(invitee_id))
RETURNING *;

-- InsertUser creates an account, or returns no row if the address has one.
-- name: InsertUser :one
INSERT INTO users (email) VALUES ($1) ON CONFLICT (email) DO NOTHING RETURNING *;

-- name: ListInvitesByInviter :many
SELECT * FROM invites WHERE inviter_id = $1 ORDER BY created_at DESC LIMIT 100;

-- ForgetInvites runs when an account is deleted: rows carrying its address
-- go, and so do the rows it sent that created no account.
-- name: ForgetInvites :exec
DELETE FROM invites
WHERE email = sqlc.arg(email) OR (inviter_id = sqlc.arg(user_id) AND invitee_id IS NULL);
