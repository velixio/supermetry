-- Durable queue for erasing one end user's data from an app project. The user
-- is held only as the keyed hash (HMAC under the project's hash secret) that
-- app_events.user_hash already carries; the raw id is never stored. A null
-- hash means the project never hashed a user, so the request is born done.
CREATE TABLE app_erasure_requests (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    user_hash bytea CHECK (user_hash IS NULL OR octet_length(user_hash) = 32),
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'done', 'failed')),
    bodies_deleted bigint NOT NULL DEFAULT 0 CHECK (bodies_deleted >= 0),
    events_anonymized bigint NOT NULL DEFAULT 0 CHECK (events_anonymized >= 0),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error text,
    requested_by text NOT NULL
        CHECK (requested_by ~ '^(user|api_key):[1-9][0-9]*$'),
    retry_after timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK (status <> 'done' OR completed_at IS NOT NULL),
    CHECK (user_hash IS NOT NULL OR status = 'done')
);

-- One live request per user: a repeat call returns it instead of queueing again.
CREATE UNIQUE INDEX app_erasure_requests_active_user_idx
    ON app_erasure_requests (site_id, user_hash)
    WHERE status <> 'done';

CREATE INDEX app_erasure_requests_worker_idx
    ON app_erasure_requests (created_at, id)
    WHERE status IN ('pending', 'running');

-- Finished requests are pruned by age.
CREATE INDEX app_erasure_requests_done_idx
    ON app_erasure_requests (completed_at)
    WHERE status = 'done';
