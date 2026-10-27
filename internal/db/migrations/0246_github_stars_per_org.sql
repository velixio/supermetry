-- GitHub star tracking becomes an organization feature. Each tracked
-- repository belongs to one organization, two organizations may track the same
-- GitHub repository independently, and the star history tables hang off the
-- repository row so that removing a repository (or its organization) removes
-- its data in one step.

-- Per-organization cap on tracked repositories. NULL means "use the instance
-- default", -1 means unlimited, 0 or more is the cap itself.
ALTER TABLE orgs
    ADD COLUMN github_star_repo_limit integer,
    ADD CONSTRAINT orgs_github_star_repo_limit_check CHECK (github_star_repo_limit IS NULL OR github_star_repo_limit >= -1);

ALTER TABLE github_star_repos
    ADD COLUMN org_id bigint REFERENCES orgs(id) ON DELETE CASCADE,
    ADD COLUMN public_counter boolean NOT NULL DEFAULT false,
    -- The star count last read from GitHub itself by the reconcile and import
    -- jobs. The public counter serves only this value, never the live count that
    -- webhook payloads (signed by whoever configured the repository) update.
    ADD COLUMN verified_stars integer,
    ADD COLUMN verified_at timestamptz;

-- Repositories that existed before belong to the lowest-id organization
-- (preferring a live one) and keep their public counter. Without any
-- organization there cannot be any repository, but clear stragglers anyway so
-- the NOT NULL below cannot fail.
UPDATE github_star_repos
SET org_id = coalesce((SELECT min(id) FROM orgs WHERE deleted_at IS NULL), (SELECT min(id) FROM orgs)),
    public_counter = true;
DELETE FROM github_star_repos WHERE org_id IS NULL;
-- Repositories that already reconciled successfully carry their stored count
-- over as the verified one; the next reconcile refreshes it from GitHub.
UPDATE github_star_repos r
SET verified_stars = st.stars, verified_at = r.last_reconcile_at
FROM github_star_state st
WHERE st.repo = r.repo AND r.last_reconcile_at IS NOT NULL AND r.last_error IS NULL;
ALTER TABLE github_star_repos ALTER COLUMN org_id SET NOT NULL;

ALTER TABLE github_star_repos DROP CONSTRAINT github_star_repos_repo_key;
ALTER TABLE github_star_repos ADD CONSTRAINT github_star_repos_org_repo_key UNIQUE (org_id, repo);
CREATE INDEX github_star_repos_repo_idx ON github_star_repos (repo);

-- Events.
ALTER TABLE github_star_events ADD COLUMN repo_id bigint;
UPDATE github_star_events e SET repo_id = r.id FROM github_star_repos r WHERE r.repo = e.repo;
DELETE FROM github_star_events WHERE repo_id IS NULL;
ALTER TABLE github_star_events
    ALTER COLUMN repo_id SET NOT NULL,
    ADD CONSTRAINT github_star_events_repo_id_fkey FOREIGN KEY (repo_id) REFERENCES github_star_repos(id) ON DELETE CASCADE,
    DROP CONSTRAINT github_star_events_delivery_id_key,
    DROP COLUMN repo;
ALTER TABLE github_star_events ADD CONSTRAINT github_star_events_repo_delivery_key UNIQUE (repo_id, delivery_id);
CREATE INDEX github_star_events_repo_time ON github_star_events (repo_id, occurred_at);
CREATE INDEX github_star_events_repo_user ON github_star_events (repo_id, user_id, occurred_at);

-- Live counters.
ALTER TABLE github_star_state ADD COLUMN repo_id bigint;
UPDATE github_star_state s SET repo_id = r.id FROM github_star_repos r WHERE r.repo = s.repo;
DELETE FROM github_star_state WHERE repo_id IS NULL;
ALTER TABLE github_star_state DROP CONSTRAINT github_star_state_pkey, DROP COLUMN repo;
ALTER TABLE github_star_state
    ALTER COLUMN repo_id SET NOT NULL,
    ADD PRIMARY KEY (repo_id),
    ADD CONSTRAINT github_star_state_repo_id_fkey FOREIGN KEY (repo_id) REFERENCES github_star_repos(id) ON DELETE CASCADE;

-- Daily snapshots.
ALTER TABLE github_star_snapshots ADD COLUMN repo_id bigint;
UPDATE github_star_snapshots s SET repo_id = r.id FROM github_star_repos r WHERE r.repo = s.repo;
DELETE FROM github_star_snapshots WHERE repo_id IS NULL;
ALTER TABLE github_star_snapshots DROP CONSTRAINT github_star_snapshots_pkey, DROP COLUMN repo;
ALTER TABLE github_star_snapshots
    ALTER COLUMN repo_id SET NOT NULL,
    ADD PRIMARY KEY (repo_id, day),
    ADD CONSTRAINT github_star_snapshots_repo_id_fkey FOREIGN KEY (repo_id) REFERENCES github_star_repos(id) ON DELETE CASCADE;

-- Annotations.
ALTER TABLE github_star_annotations ADD COLUMN repo_id bigint;
UPDATE github_star_annotations a SET repo_id = r.id FROM github_star_repos r WHERE r.repo = a.repo;
DELETE FROM github_star_annotations WHERE repo_id IS NULL;
ALTER TABLE github_star_annotations
    ALTER COLUMN repo_id SET NOT NULL,
    ADD CONSTRAINT github_star_annotations_repo_id_fkey FOREIGN KEY (repo_id) REFERENCES github_star_repos(id) ON DELETE CASCADE,
    DROP COLUMN repo;
CREATE INDEX github_star_annotations_repo_at ON github_star_annotations (repo_id, at);

-- Erasure tombstones.
ALTER TABLE github_star_erasures ADD COLUMN repo_id bigint;
UPDATE github_star_erasures e SET repo_id = r.id FROM github_star_repos r WHERE r.repo = e.repo;
DELETE FROM github_star_erasures WHERE repo_id IS NULL;
ALTER TABLE github_star_erasures DROP CONSTRAINT github_star_erasures_pkey, DROP COLUMN repo;
ALTER TABLE github_star_erasures
    ALTER COLUMN repo_id SET NOT NULL,
    ADD PRIMARY KEY (repo_id, user_id),
    ADD CONSTRAINT github_star_erasures_repo_id_fkey FOREIGN KEY (repo_id) REFERENCES github_star_repos(id) ON DELETE CASCADE;
