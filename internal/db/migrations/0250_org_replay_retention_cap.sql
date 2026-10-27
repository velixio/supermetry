-- Per-organization cap on replay retention, in days. NULL means the
-- organization has no cap of its own (the instance cap and the site's own
-- setting still apply). Set by an instance admin so a hosted deployment can
-- give each plan its own replay retention.
ALTER TABLE orgs
    ADD COLUMN replay_retention_cap_days integer,
    ADD CONSTRAINT orgs_replay_retention_cap_days_check
        CHECK (replay_retention_cap_days IS NULL OR replay_retention_cap_days BETWEEN 1 AND 365);
