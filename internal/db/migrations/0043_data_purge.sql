-- Owner-requested deletion of stored traffic by site-local date range and a
-- source and/or country, for removing referrer spam or unwanted traffic.
CREATE TABLE data_purge_requests (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    from_day date NOT NULL,
    to_day date NOT NULL,
    source text NOT NULL DEFAULT '',
    country text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'settling', 'done', 'failed')),
    requested_by text NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    last_error text,
    sessions_deleted bigint NOT NULL DEFAULT 0,
    settle_after timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK (from_day <= to_day),
    CHECK (source <> '' OR country <> '')
);
CREATE UNIQUE INDEX data_purge_one_active_per_site
    ON data_purge_requests (site_id) WHERE status IN ('pending', 'running', 'settling');
CREATE INDEX data_purge_requests_site_idx ON data_purge_requests (site_id, id DESC);

-- Sessions selected by a request. Kept until the request is deleted so the
-- later cleanup passes can use the same set.
CREATE TABLE data_purge_sessions (
    request_id bigint NOT NULL REFERENCES data_purge_requests(id) ON DELETE CASCADE,
    session_id bigint NOT NULL,
    visitor_id bigint NOT NULL,
    local_day date NOT NULL,
    first_ts timestamptz NOT NULL,
    last_ts timestamptz NOT NULL,
    PRIMARY KEY (request_id, session_id, local_day)
);
