-- Deploys belong to the organisation and to the projects they name, and carry
-- the instant they took effect, so the dashboard can mark them on a time axis.
-- An empty site_ids list falls back to the release's own projects (and, for an
-- org-wide release, to every project of the org).
ALTER TABLE app_deploys
    ADD COLUMN org_id   bigint NULL REFERENCES orgs(id) ON DELETE CASCADE,
    ADD COLUMN site_ids integer[] NOT NULL DEFAULT '{}';

UPDATE app_deploys d SET org_id = r.org_id FROM app_releases r WHERE r.id = d.release_id;

ALTER TABLE app_deploys ALTER COLUMN org_id SET NOT NULL;

ALTER TABLE app_deploys
    ADD COLUMN at timestamptz GENERATED ALWAYS AS (coalesce(date_finished, date_started, date_created)) STORED;

CREATE INDEX app_deploys_org_at ON app_deploys (org_id, at);
