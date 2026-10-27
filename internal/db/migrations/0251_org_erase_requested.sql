-- Set when an instance admin erases an organization without waiting for the
-- deletion grace period. The erasure job treats such an organization (and its
-- sites, which carry erasure_started_at) as due now, and nothing can restore it.
ALTER TABLE orgs
    ADD COLUMN erase_requested_at timestamptz;
