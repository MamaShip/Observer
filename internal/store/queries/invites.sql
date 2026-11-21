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

-- ListInvitesByInviter says, for each invitation, whether the account it
-- created has logged in. An invitation to an address that already had an
-- account created none and so never counts as accepted.
-- name: ListInvitesByInviter :many
SELECT i.id, i.email, i.created_at, (u.last_login_at IS NOT NULL)::bool AS accepted
FROM invites i LEFT JOIN users u ON u.id = i.invitee_id
WHERE i.inviter_id = $1 ORDER BY i.created_at DESC LIMIT 100;

-- name: GetInviteForUpdate :one
SELECT * FROM invites WHERE id = sqlc.arg(id) AND inviter_id = sqlc.arg(inviter_id) FOR UPDATE;

-- name: DeleteInvite :exec
DELETE FROM invites WHERE id = $1;

-- PassInvitee hands an account to the oldest other invitation of its
-- address, once the invitation that created it is withdrawn.
-- name: PassInvitee :execrows
UPDATE invites SET invitee_id = sqlc.arg(invitee_id)
WHERE id = (SELECT o.id FROM invites o WHERE o.email = sqlc.arg(email) AND o.invitee_id IS NULL
            ORDER BY o.created_at LIMIT 1);

-- DeleteUnusedAccount removes an account that has never logged in.
-- name: DeleteUnusedAccount :execrows
DELETE FROM users WHERE id = $1 AND last_login_at IS NULL;

-- ForgetInvites runs when an account is deleted: rows carrying its address
-- go, and so do the rows it sent that created no account.
-- name: ForgetInvites :exec
DELETE FROM invites
WHERE email = sqlc.arg(email) OR (inviter_id = sqlc.arg(user_id) AND invitee_id IS NULL);
