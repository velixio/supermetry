-- One-off silences and recurring maintenance windows for alert rules. A row
-- with a NULL alert_id covers every rule of the site. Maintenance windows are
-- wall-clock times in their own time zone (tz), so a DST switch keeps the
-- window at the same local time.

CREATE TABLE alert_silences (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id          integer     NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    alert_id         bigint      REFERENCES alerts(id) ON DELETE CASCADE,
    kind             text        NOT NULL CHECK (kind IN ('silence', 'maintenance')),
    starts_at        timestamptz NOT NULL,
    ends_at          timestamptz,
    weekdays         smallint[]  CHECK (weekdays IS NULL OR (cardinality(weekdays) BETWEEN 1 AND 7 AND weekdays <@ ARRAY[0, 1, 2, 3, 4, 5, 6]::smallint[])),
    start_time       time,
    duration_minutes integer     CHECK (duration_minutes BETWEEN 1 AND 1440),
    tz               text,
    reason           text        NOT NULL DEFAULT '' CHECK (char_length(reason) <= 200),
    created_by       bigint      REFERENCES users(id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (kind = 'silence' AND ends_at IS NOT NULL AND weekdays IS NULL AND start_time IS NULL AND duration_minutes IS NULL AND tz IS NULL)
        OR
        (kind = 'maintenance' AND weekdays IS NOT NULL AND start_time IS NOT NULL AND duration_minutes IS NOT NULL AND tz IS NOT NULL)
    )
);

CREATE INDEX alert_silences_site_idx ON alert_silences (site_id);
CREATE INDEX alert_silences_alert_idx ON alert_silences (alert_id) WHERE alert_id IS NOT NULL;

-- A suppressed notification is logged without a channel.
ALTER TABLE alert_delivery_log DROP CONSTRAINT alert_delivery_log_status_check;
ALTER TABLE alert_delivery_log ADD CONSTRAINT alert_delivery_log_status_check
    CHECK (status IN ('pending', 'succeeded', 'failed', 'suppressed'));
ALTER TABLE alert_delivery_log DROP CONSTRAINT alert_delivery_log_channel_kind_check;
ALTER TABLE alert_delivery_log ADD CONSTRAINT alert_delivery_log_channel_kind_check
    CHECK (channel_kind IN ('email', 'webhook', 'slack', 'discord', 'telegram', 'none'));
