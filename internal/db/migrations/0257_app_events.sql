CREATE TABLE app_events (
    site_id       integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    ts            timestamptz NOT NULL,
    received_at   timestamptz NOT NULL,
    event_id      text NOT NULL,
    issue_id      bigint NULL,
    kind          text NOT NULL CHECK (kind IN (
        'crash', 'error', 'hang', 'install_failed', 'install_stalled',
        'incompatibility', 'operation_failed', 'session'
    )),
    level         text NOT NULL DEFAULT '',
    handled       boolean NOT NULL DEFAULT false,
    release       text NOT NULL DEFAULT '',
    build         text NOT NULL DEFAULT '',
    environment   text NOT NULL DEFAULT '',
    platform      text NOT NULL DEFAULT '',
    os_version    text NOT NULL DEFAULT '',
    arch          text NOT NULL DEFAULT '',
    device_class  text NOT NULL DEFAULT '',
    runtime       text NOT NULL DEFAULT '',
    error_type    text NOT NULL DEFAULT '',
    exit_reason   text NOT NULL DEFAULT '',
    signal        text NOT NULL DEFAULT '',
    exit_code     integer NULL,
    frames        text NOT NULL DEFAULT '',
    fingerprint   text NOT NULL DEFAULT '',
    occurrences   integer NOT NULL DEFAULT 1 CHECK (occurrences >= 1),
    install_hash  bytea NULL,
    session_hash  bytea NULL,
    tags          jsonb NOT NULL DEFAULT '{}'::jsonb
) PARTITION BY RANGE (ts);

CREATE INDEX app_events_ts_brin ON app_events USING brin (ts) WITH (pages_per_range = 8, autosummarize = on);
CREATE INDEX app_events_issue_idx ON app_events (site_id, issue_id, ts);
