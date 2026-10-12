-- Third-party integrations: one connection per site and provider, short-lived
-- OAuth authorization states, and the imported Search Console daily rows.

CREATE TABLE integration_connections (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    provider text NOT NULL CHECK (provider ~ '^[a-z][a-z0-9-]{1,31}$'),
    property text NOT NULL CHECK (octet_length(property) BETWEEN 1 AND 512),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'reauth')),
    -- Sealed JSON (access token, refresh token, expiry); never plaintext.
    credentials bytea NOT NULL CHECK (octet_length(credentials) BETWEEN 1 AND 8192),
    connected_by bigint REFERENCES users(id) ON DELETE SET NULL,
    synced_through date,
    backfill_from date NOT NULL,
    last_sync_at timestamptz,
    last_error text NOT NULL DEFAULT '' CHECK (octet_length(last_error) <= 256),
    failure_count integer NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (site_id, provider)
);

CREATE INDEX integration_connections_due_idx
    ON integration_connections (next_attempt_at) WHERE status = 'active';

CREATE TABLE integration_oauth_states (
    state_hash bytea PRIMARY KEY CHECK (octet_length(state_hash) = 32),
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    org_id bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    provider text NOT NULL,
    property text NOT NULL CHECK (octet_length(property) BETWEEN 1 AND 512),
    backfill_days smallint NOT NULL CHECK (backfill_days BETWEEN 1 AND 485),
    code_verifier bytea NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE INDEX integration_oauth_states_expiry_idx ON integration_oauth_states (expires_at);

-- The primary key is a 16-byte digest of the dimension values so very long
-- queries and URLs cannot exceed the btree key limit; reruns for a day replace
-- the day's rows, so the table never holds duplicates.
CREATE TABLE search_console_rows (
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    day date NOT NULL,
    row_key bytea NOT NULL CHECK (octet_length(row_key) = 16),
    query text NOT NULL CHECK (octet_length(query) <= 1024),
    page text NOT NULL CHECK (octet_length(page) <= 2048),
    country text NOT NULL CHECK (octet_length(country) <= 8),
    device text NOT NULL CHECK (octet_length(device) <= 16),
    clicks integer NOT NULL CHECK (clicks >= 0),
    impressions integer NOT NULL CHECK (impressions >= 0),
    ctr real NOT NULL,
    position real NOT NULL,
    PRIMARY KEY (site_id, day, row_key)
);
