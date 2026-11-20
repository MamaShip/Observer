-- Invitations (plan.md §6.1). Every invitation a user sends is recorded,
-- including one to an address that already had an account, so the monthly
-- allowance counts attempts and its answers reveal nothing about who is
-- registered. invitee_id is set only when the invitation created the account:
-- that row is the "invited by" relationship. Deleting an account removes the
-- rows that carry its address; the rows it sent stay with inviter_id NULL
-- when they created an account, the others go.
-- +goose Up
CREATE TABLE invites (
  id         uuid PRIMARY KEY DEFAULT uuidv7(),
  inviter_id uuid REFERENCES users (id) ON DELETE SET NULL,
  email      citext NOT NULL,
  invitee_id uuid UNIQUE REFERENCES users (id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX invites_inviter ON invites (inviter_id, created_at DESC);
CREATE INDEX invites_email ON invites (email);

-- +goose Down
DROP TABLE invites;
