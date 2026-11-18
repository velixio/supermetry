-- Cron monitors and their check-ins (Sentry check_in envelope items).
-- app_monitors is one row per (site, slug, environment) and carries the
-- state machine (status, consecutive counters, next expected slot).
-- app_checkins is the append-mostly log, partitioned monthly by ts like
-- app_events and dropped by retention; monitor_id is not a foreign key so a
-- partition never blocks a monitor delete (site deletion cascades via site_id).
CREATE TABLE app_monitors (
    id                    bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id               integer     NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    slug                  text        NOT NULL CHECK (slug ~ '^[a-z0-9_-]{1,64}$'),
    environment           text        NOT NULL DEFAULT 'production' CHECK (char_length(environment) <= 64),
    schedule_type         text        NOT NULL CHECK (schedule_type IN ('crontab', 'interval')),
    schedule              text        NOT NULL CHECK (char_length(schedule) BETWEEN 1 AND 200),
    tz                    text        NOT NULL DEFAULT 'UTC' CHECK (char_length(tz) <= 64),
    checkin_margin_minutes int        NOT NULL DEFAULT 1 CHECK (checkin_margin_minutes BETWEEN 1 AND 1440),
    max_runtime_minutes   int         NOT NULL DEFAULT 30 CHECK (max_runtime_minutes BETWEEN 1 AND 1440),
    failure_threshold     int         NOT NULL DEFAULT 1 CHECK (failure_threshold BETWEEN 1 AND 100),
    recovery_threshold    int         NOT NULL DEFAULT 1 CHECK (recovery_threshold BETWEEN 1 AND 100),
    status                text        NOT NULL DEFAULT 'ok' CHECK (status IN ('ok', 'missed', 'error', 'timeout', 'disabled')),
    consecutive_failures  int         NOT NULL DEFAULT 0,
    consecutive_ok        int         NOT NULL DEFAULT 0,
    next_expected_at      timestamptz,
    last_checkin_at       timestamptz,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    UNIQUE (site_id, slug, environment)
);
CREATE INDEX app_monitors_due_idx ON app_monitors (next_expected_at) WHERE status <> 'disabled';

-- failure_status and recovery_at mark the row whose arrival crossed a
-- threshold (the status that opened the failure, the time the monitor
-- recovered); the alert-rules job turns each into one notification. They are
-- separate from status because a late ok completes a failed row in place.
CREATE TABLE app_checkins (
    site_id        integer     NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    monitor_id     bigint      NOT NULL,
    ts             timestamptz NOT NULL,
    checkin_id     uuid        NOT NULL,
    status         text        NOT NULL CHECK (status IN ('in_progress', 'ok', 'error', 'missed', 'timeout')),
    expected_at    timestamptz,
    duration_ms    bigint      CHECK (duration_ms IS NULL OR duration_ms >= 0),
    release        text,
    failure_status text        CHECK (failure_status IN ('error', 'missed', 'timeout')),
    recovery_at    timestamptz,
    PRIMARY KEY (site_id, checkin_id, ts)
) PARTITION BY RANGE (ts);

CREATE INDEX app_checkins_ts_brin ON app_checkins USING brin (ts) WITH (pages_per_range = 8, autosummarize = on);
CREATE INDEX app_checkins_monitor_idx ON app_checkins (monitor_id, ts DESC);
-- The timeout sweep only looks at runs that have not finished.
CREATE INDEX app_checkins_in_progress_idx ON app_checkins (ts) WHERE status = 'in_progress';
