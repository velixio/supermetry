-- Alert rules (trigger + filters + actions) for app projects, stored in the
-- alerts table with kind 'app_rule', plus per-group incidents for metric and
-- monitor triggers. The four v1 app kinds are converted in place so alert ids
-- (and the app_alert_fired keys that hang off them) stay valid.

ALTER TABLE alerts DROP CONSTRAINT alerts_kind_check;
ALTER TABLE alerts ADD CONSTRAINT alerts_kind_check
    CHECK (kind IN ('traffic_spike', 'traffic_drop', 'goal_reached',
                    'app_new_issue', 'app_issue_regressed', 'app_crash_free_below', 'app_issue_spike',
                    'app_rule'));

ALTER TABLE alerts ADD COLUMN name text NOT NULL DEFAULT '' CHECK (char_length(name) <= 120);
ALTER TABLE alerts ADD COLUMN rule jsonb CHECK (rule IS NULL OR jsonb_typeof(rule) = 'object');

-- A rule may only assign or page by owner, so it can have no channel.
ALTER TABLE alerts DROP CONSTRAINT alerts_channel_ids_check;
ALTER TABLE alerts ADD CONSTRAINT alerts_channel_ids_check
    CHECK (NOT enabled OR kind = 'app_rule' OR cardinality(channel_ids) > 0);

-- One row per firing group (release or whole site) of a metric or monitor
-- rule; at most one open row per group.
CREATE TABLE alert_incidents (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    alert_id       bigint           NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    site_id        integer          NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    group_key      text             NOT NULL CHECK (char_length(group_key) BETWEEN 1 AND 200),
    severity       text             NOT NULL CHECK (severity IN ('warning', 'critical')),
    opened_at      timestamptz      NOT NULL,
    resolved_at    timestamptz,
    last_value     double precision NOT NULL,
    peak_value     double precision NOT NULL,
    threshold      double precision NOT NULL,
    notified_at    timestamptz,
    renotified_at  timestamptz
);
CREATE UNIQUE INDEX alert_incidents_open_group_idx ON alert_incidents (alert_id, group_key) WHERE resolved_at IS NULL;
CREATE INDEX alert_incidents_site_opened_idx ON alert_incidents (site_id, opened_at DESC);

-- Convert the v1 app alerts. The channels move into one channel action.
UPDATE alerts SET
    kind = 'app_rule',
    name = CASE kind
        WHEN 'app_new_issue' THEN 'New issue'
        WHEN 'app_issue_regressed' THEN 'Regressed issue'
        WHEN 'app_issue_spike' THEN 'Issue spike'
        ELSE 'Crash-free rate below threshold' END,
    rule = jsonb_build_object(
        'trigger', CASE kind
            WHEN 'app_new_issue' THEN '{"type":"issue","issue":{"on":["new"]}}'::jsonb
            WHEN 'app_issue_regressed' THEN '{"type":"issue","issue":{"on":["regressed"]}}'::jsonb
            WHEN 'app_issue_spike' THEN '{"type":"event","event":{"spike":{"factor":3,"min_events":10,"baseline_days":7}}}'::jsonb
            ELSE jsonb_build_object('type', 'metric', 'metric', jsonb_build_object(
                'name', 'crash_free_' || coalesce(config->>'metric', 'users'),
                'op', 'below', 'critical', threshold, 'window_minutes', 1440,
                'min_sample', 100, 'release', 'latest', 'renotify_minutes', 1440)) END,
        'filters', CASE
            WHEN kind = 'app_new_issue' AND jsonb_array_length(coalesce(config->'kinds', '[]'::jsonb)) > 0
                THEN jsonb_build_object('kinds', config->'kinds')
            ELSE '{}'::jsonb END,
        'actions', jsonb_build_array(jsonb_build_object('type', 'channel', 'channel_ids', to_jsonb(channel_ids))))
        || CASE kind WHEN 'app_crash_free_below' THEN '{"notify_on_resolve":false}'::jsonb ELSE '{}'::jsonb END
WHERE kind IN ('app_new_issue', 'app_issue_regressed', 'app_issue_spike', 'app_crash_free_below');

-- A crash-free key fired in the last 24 h is an open incident that was already
-- notified, so the converted rule stays quiet for the rest of its cooldown.
-- The value columns are not recorded by v1; the threshold stands in.
INSERT INTO alert_incidents (alert_id, site_id, group_key, severity, opened_at, last_value, peak_value, threshold, notified_at)
SELECT f.alert_id, a.site_id, 'release:' || substr(f.dedup_key, 11), 'critical', f.fired_at,
       a.threshold, a.threshold, a.threshold, f.fired_at
FROM app_alert_fired f
JOIN alerts a ON a.id = f.alert_id
WHERE f.dedup_key LIKE 'crashfree:%'
  AND char_length(f.dedup_key) > 10
  AND f.fired_at > now() - interval '24 hours'
  AND a.kind = 'app_rule'
  AND a.rule->'trigger'->>'type' = 'metric';
