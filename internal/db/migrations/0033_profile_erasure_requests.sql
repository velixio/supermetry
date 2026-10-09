CREATE TABLE profile_erasure_requests (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    profile_id bigint NOT NULL,
    external_hash bytea NOT NULL CHECK (octet_length(external_hash) = 32),
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'settling', 'done', 'failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error text,
    requested_by text NOT NULL
        CHECK (requested_by ~ '^(user|api_key):[1-9][0-9]*$'),
    settle_after timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK (status <> 'done' OR completed_at IS NOT NULL)
);

CREATE UNIQUE INDEX profile_erasure_requests_active_identity_idx
    ON profile_erasure_requests (site_id, external_hash)
    WHERE status <> 'done';

CREATE INDEX profile_erasure_requests_worker_idx
    ON profile_erasure_requests (created_at, id)
    WHERE status IN ('pending', 'running', 'settling');

CREATE TABLE profile_erasure_blobs (
    request_id bigint NOT NULL REFERENCES profile_erasure_requests(id) ON DELETE CASCADE,
    blob_key text NOT NULL CHECK (blob_key <> ''),
    deleted_at timestamptz,
    PRIMARY KEY (request_id, blob_key)
);

CREATE TABLE profile_erasure_sessions (
    request_id bigint NOT NULL REFERENCES profile_erasure_requests(id) ON DELETE CASCADE,
    session_id bigint NOT NULL,
    visitor_id bigint NOT NULL,
    local_day date NOT NULL,
    first_ts timestamptz NOT NULL,
    last_ts timestamptz NOT NULL,
    PRIMARY KEY (request_id, session_id, local_day)
);
