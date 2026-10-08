CREATE TABLE replay_sessions (
    site_id integer NOT NULL,
    session_id bigint NOT NULL,
    started_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    day date NOT NULL,
    bytes bigint NOT NULL DEFAULT 0,
    pages integer NOT NULL DEFAULT 0,
    trigger text,
    profile_id bigint,
    blob_key text NOT NULL,
    PRIMARY KEY (site_id, started_at, session_id)
) PARTITION BY RANGE (started_at);

CREATE INDEX replay_sessions_started_brin_idx
    ON replay_sessions USING brin (started_at);

CREATE INDEX replay_sessions_site_session_idx
    ON replay_sessions (site_id, session_id);
