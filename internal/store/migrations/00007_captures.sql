-- Pages fetched to be backed up (plan.md §6.3). Each row is a raw page
-- stored in object storage and the reading page built from it: pending until
-- the snapshot job finishes, then done (the snapshot has this row's id) or
-- failed with a reason. A failed one can be rebuilt from the stored page
-- without fetching the article again, even after it was deleted.
-- +goose Up
CREATE TABLE captures (
  id          uuid PRIMARY KEY, -- the snapshot's id once it is built
  article_id  uuid NOT NULL REFERENCES articles (id) ON DELETE CASCADE,
  raw_key     text NOT NULL,
  url         text NOT NULL,   -- the article link, sent as Referer for images
  captured_at timestamptz NOT NULL,
  status      text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done', 'failed')),
  -- no_content: the page has no article body (picture or video posts);
  -- error: the build kept failing until its retries ran out
  error_code  text CHECK (error_code IN ('no_content', 'error')),
  error       text,
  updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX captures_article ON captures (article_id, captured_at DESC);

-- Every existing snapshot came from a capture that is done.
INSERT INTO captures (id, article_id, raw_key, url, captured_at, status, updated_at)
SELECT s.id, s.article_id, s.raw_key, coalesce(a.canonical_url, ''), s.captured_at, 'done', s.captured_at
FROM snapshots s JOIN articles a ON a.id = s.article_id;

-- +goose Down
DROP TABLE captures;
