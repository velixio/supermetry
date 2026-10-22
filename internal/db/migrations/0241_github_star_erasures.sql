-- Users whose star history was erased on request. Only the numeric GitHub user
-- id is kept (no login), so the webhook, reconcile and backfill can skip them
-- instead of re-recording the star that GitHub still lists.
CREATE TABLE github_star_erasures (
    repo      text NOT NULL,
    user_id   bigint NOT NULL,
    erased_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repo, user_id)
);
