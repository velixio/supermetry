ALTER TABLE sites ALTER COLUMN demo_started_at SET DEFAULT now();
UPDATE sites SET demo_started_at = coalesce(demo_seeded_at, now()) WHERE is_demo AND demo_started_at IS NULL;
