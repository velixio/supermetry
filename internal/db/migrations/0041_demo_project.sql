ALTER TABLE sites ADD COLUMN is_demo boolean NOT NULL DEFAULT false;
ALTER TABLE sites ADD COLUMN demo_seeded_at timestamptz;
ALTER TABLE sites ADD COLUMN demo_live_through timestamptz;
ALTER TABLE sites ADD COLUMN demo_viewed_at timestamptz;
CREATE UNIQUE INDEX sites_one_demo_per_org ON sites (org_id) WHERE is_demo AND deleted_at IS NULL;
ALTER TABLE users ADD COLUMN demo_prompt_dismissed_at timestamptz;
