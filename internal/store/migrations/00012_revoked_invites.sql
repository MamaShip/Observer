-- Revoking an invitation whose email was already queued no longer gives its
-- place in the monthly allowance back (plan.md §6.1): otherwise inviting and
-- revoking in turn would send unlimited email to arbitrary addresses. Each
-- such revocation leaves a row here, without the address, dated like the
-- invitation it replaces.
-- +goose Up
CREATE TABLE revoked_invites (
  id         uuid PRIMARY KEY DEFAULT uuidv7(),
  inviter_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  invited_at timestamptz NOT NULL
);
CREATE INDEX revoked_invites_inviter ON revoked_invites (inviter_id, invited_at);

-- +goose Down
DROP TABLE revoked_invites;
