-- Public invitation links (plan.md §6.1). An admin opens a link for a number
-- of new accounts and a period; anyone holding it can sign up by proving
-- their address with a login code. The token is derived from the id with
-- the signing key, so nothing secret is stored. used counts accounts
-- created and never goes down, so deleting one does not free a place.
-- codes_sent caps the login codes the link can make us send.
-- users.invite_link_id records which link created an account; private
-- invitations stay in invites.
-- +goose Up
CREATE TABLE invite_links (
  id         uuid PRIMARY KEY DEFAULT uuidv7(),
  creator_id uuid REFERENCES users (id) ON DELETE SET NULL,
  note       text NOT NULL DEFAULT '' CHECK (length(note) <= 100),
  max_uses   int NOT NULL CHECK (max_uses BETWEEN 1 AND 1000),
  used       int NOT NULL DEFAULT 0,
  codes_sent int NOT NULL DEFAULT 0,
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX invite_links_created ON invite_links (created_at DESC);

ALTER TABLE users ADD COLUMN invite_link_id uuid REFERENCES invite_links (id) ON DELETE SET NULL;
CREATE INDEX users_invite_link ON users (invite_link_id) WHERE invite_link_id IS NOT NULL;

-- +goose Down
ALTER TABLE users DROP COLUMN invite_link_id;
DROP TABLE invite_links;
