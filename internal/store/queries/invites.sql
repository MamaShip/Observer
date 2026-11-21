-- LockUser serialises one user's invitations so the monthly count holds.
-- name: LockUser :one
SELECT * FROM users WHERE id = $1 FOR UPDATE;

-- CountInvitesSince also counts revoked invitations that had queued an email.
-- name: CountInvitesSince :one
SELECT ((SELECT count(*) FROM invites
         WHERE inviter_id = sqlc.arg(inviter_id)::uuid AND created_at >= sqlc.arg(since)::timestamptz)
      + (SELECT count(*) FROM revoked_invites
         WHERE inviter_id = sqlc.arg(inviter_id)::uuid AND invited_at >= sqlc.arg(since)::timestamptz))::int;

-- name: GetInviteByInviter :one
SELECT sqlc.embed(i), (u.last_login_at IS NOT NULL)::bool AS accepted
FROM invites i LEFT JOIN users u ON u.id = i.invitee_id
WHERE i.inviter_id IS NOT DISTINCT FROM sqlc.narg(inviter_id) AND i.email = sqlc.arg(email)
ORDER BY i.created_at DESC LIMIT 1;

-- name: CreateInvite :one
INSERT INTO invites (inviter_id, email, invitee_id)
VALUES (sqlc.narg(inviter_id), sqlc.arg(email), sqlc.narg(invitee_id))
RETURNING *;

-- ReserveInvitedAccount locks the address through a concurrent invite or revocation.
-- PostgreSQL 18 exposes the old row on conflict and NULL on insert.
-- name: ReserveInvitedAccount :one
INSERT INTO users (email) VALUES ($1) ON CONFLICT (email) DO UPDATE SET email = users.email
RETURNING id, (old.id IS NULL)::bool AS created;

-- ListInvitesByInviter says, for each invitation, whether the account it
-- created has logged in. An invitation to an address that already had an
-- account created none and so never counts as accepted.
-- name: ListInvitesByInviter :many
SELECT i.id, i.email, i.created_at, (u.last_login_at IS NOT NULL)::bool AS accepted
FROM invites i LEFT JOIN users u ON u.id = i.invitee_id
WHERE i.inviter_id = $1 ORDER BY i.created_at DESC LIMIT 100;

-- name: GetOwnedInviteEmail :one
SELECT email FROM invites WHERE id = sqlc.arg(id) AND inviter_id = sqlc.arg(inviter_id);

-- name: GetInviteForUpdate :one
SELECT * FROM invites WHERE id = sqlc.arg(id) AND inviter_id = sqlc.arg(inviter_id) FOR UPDATE;

-- name: DeleteInvite :exec
DELETE FROM invites WHERE id = $1;

-- name: RecordRevokedInvite :exec
INSERT INTO revoked_invites (inviter_id, invited_at) VALUES (sqlc.arg(inviter_id), sqlc.arg(invited_at));

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

-- name: QueueInviteMailOnce :execrows
UPDATE invites SET mail_queued = true WHERE id = $1 AND NOT mail_queued;

-- RequeueExpiredInviteMail lets a repeated invitation send a fresh link once
-- the last one expired unused. It looks only at the invitation, so the answer
-- is the same whether or not the address has an account.
-- name: RequeueExpiredInviteMail :execrows
UPDATE invites SET token_hash = NULL, token_expires_at = NULL, mail_sent_at = NULL
WHERE id = sqlc.arg(id) AND token_expires_at <= sqlc.arg(now)::timestamptz;

-- name: PrepareInviteMail :one
UPDATE invites i SET token_hash = sqlc.arg(token_hash), token_expires_at = sqlc.arg(expires_at)::timestamptz
FROM users u WHERE i.id = sqlc.arg(id) AND i.mail_sent_at IS NULL
  AND u.email = i.email AND u.status = 'active' AND u.last_login_at IS NULL
RETURNING i.email;

-- name: MarkInviteMailed :exec
UPDATE invites SET mail_sent_at = now() WHERE id = $1;

-- name: GetInviteByToken :one
SELECT id, email FROM invites WHERE token_hash = $1;

-- name: ConsumeInviteToken :execrows
UPDATE invites SET token_hash = NULL, token_expires_at = NULL
WHERE id = sqlc.arg(id) AND token_hash = sqlc.arg(token_hash) AND token_expires_at > sqlc.arg(now)::timestamptz;

-- name: LastInviteMail :one
SELECT sent_at FROM email_log WHERE to_email = $1 AND kind = 'invite' AND status = 'sent'
ORDER BY sent_at DESC LIMIT 1;
