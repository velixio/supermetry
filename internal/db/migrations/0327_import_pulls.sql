-- Pull imports: aggregates fetched from another analytics service's API
-- (Plausible Stats API, Matomo Reporting API, GA4 Data API) instead of read
-- from an uploaded file. A pull is an ordinary imports row plus one
-- import_pulls row that records the request, the progress and a cursor, so a
-- run interrupted by a restart resumes from the last completed chunk.

ALTER TABLE imports DROP CONSTRAINT IF EXISTS imports_source_check;
ALTER TABLE imports ADD CONSTRAINT imports_source_check
    CHECK (source IN ('plausible', 'umami', 'ga4', 'mixpanel', 'amplitude', 'matomo'));

-- GA4 connection: a refresh token sealed with the application secret key and
-- scoped to the organization. Tokens are never stored in plaintext.
CREATE TABLE import_ga4_connections (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    refresh_token bytea NOT NULL CHECK (octet_length(refresh_token) BETWEEN 1 AND 4096),
    connected_by bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX import_ga4_connections_org_idx ON import_ga4_connections (org_id);

-- One-time OAuth authorization states bound to the user and organization.
CREATE TABLE import_ga4_states (
    state_hash bytea PRIMARY KEY CHECK (octet_length(state_hash) = 32),
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    org_id bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    code_verifier bytea NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE INDEX import_ga4_states_expiry_idx ON import_ga4_states (expires_at);

CREATE TABLE import_pulls (
    import_id bigint PRIMARY KEY REFERENCES imports(id) ON DELETE CASCADE,
    method text NOT NULL CHECK (method IN ('plausible_api', 'matomo_api', 'ga4_api')),
    -- Request parameters without secrets (base URL, remote site, property...).
    config jsonb NOT NULL,
    -- Sealed API key or token_auth; cleared once the pull ends.
    credentials bytea CHECK (credentials IS NULL OR octet_length(credentials) BETWEEN 1 AND 4096),
    ga4_connection_id bigint REFERENCES import_ga4_connections(id) ON DELETE SET NULL,
    range_from date NOT NULL,
    range_to date NOT NULL,
    -- Last civil day whose chunk is fully stored; null before the first chunk.
    cursor_day date,
    total_days integer NOT NULL CHECK (total_days >= 1),
    done_days integer NOT NULL DEFAULT 0 CHECK (done_days >= 0),
    rows_done bigint NOT NULL DEFAULT 0 CHECK (rows_done >= 0),
    bad_rows bigint NOT NULL DEFAULT 0 CHECK (bad_rows >= 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (range_from <= range_to)
);
