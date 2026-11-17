-- One row per Sentry session, keeping the worst status seen so far. Updates of
-- a session upsert into the same row, so a session that crashes after its init
-- is no longer counted as healthy. Hashes are HMACs under the app's secret;
-- the raw sid and did are never stored. Monthly partitions like app_events;
-- retention is by partition drop.
CREATE TABLE app_sessions (
    site_id      integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    started      timestamptz NOT NULL,
    session_hash bytea NOT NULL,
    install_hash bytea NULL,
    release      text NOT NULL DEFAULT '',
    environment  text NOT NULL DEFAULT '',
    platform     text NOT NULL DEFAULT '',
    status       text NOT NULL CHECK (status IN ('ok', 'exited', 'errored', 'abnormal', 'crashed')),
    errors       integer NOT NULL DEFAULT 0 CHECK (errors >= 0),
    duration_ms  bigint NULL CHECK (duration_ms >= 0),
    updated_at   timestamptz NOT NULL,
    PRIMARY KEY (site_id, session_hash, started)
) PARTITION BY RANGE (started);

CREATE INDEX app_sessions_started_brin ON app_sessions USING brin (started) WITH (pages_per_range = 8, autosummarize = on);
