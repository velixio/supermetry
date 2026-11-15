-- When the writer last flipped the issue to regressed; app_issue_regressed
-- alerts poll for recent values.
ALTER TABLE app_issues ADD COLUMN regressed_at timestamptz NULL;
