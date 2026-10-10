ALTER TABLE sites ADD COLUMN demo_started_at timestamptz;
UPDATE sites SET demo_started_at = coalesce(demo_seeded_at, now()) WHERE is_demo AND demo_started_at IS NULL;
