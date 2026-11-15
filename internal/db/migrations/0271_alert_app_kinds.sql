-- App alert kinds and their per-kind config (issue-kind filter, crash-free
-- metric). Kinds without a threshold store 1.
ALTER TABLE alerts DROP CONSTRAINT alerts_kind_check;
ALTER TABLE alerts ADD CONSTRAINT alerts_kind_check
    CHECK (kind IN ('traffic_spike', 'traffic_drop', 'goal_reached',
                    'app_new_issue', 'app_issue_regressed', 'app_crash_free_below', 'app_issue_spike'));
ALTER TABLE alerts ADD COLUMN config jsonb
    CHECK (config IS NULL OR jsonb_typeof(config) = 'object');
