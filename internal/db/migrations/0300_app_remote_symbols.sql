-- Debug symbols fetched from a public symbol server (Electron's), cached for
-- every organization at once: the rows are global, not per org, and live
-- outside every org's storage cap. The cache blob (a symcache built from the
-- downloaded Breakpad .sym file) is stored at remote-symbols/<source>/<debug_id>.
-- A 'missing' or 'error' row is a negative cache entry: fetched_at tells when
-- the lookup may be retried. Least-recently-used 'ok' rows are evicted by the
-- artifact sweep once their bytes pass SUPERMETRY_REMOTE_SYMBOLS_MB.
CREATE TABLE app_remote_symbols (
    source       text        NOT NULL,
    debug_file   text        NOT NULL,
    debug_id     text        NOT NULL,
    state        text        NOT NULL CHECK (state IN ('ok', 'missing', 'error')),
    cache_key    text        NULL,
    size         bigint      NOT NULL DEFAULT 0,
    fetched_at   timestamptz NOT NULL,
    last_used_at timestamptz NOT NULL,
    PRIMARY KEY (source, debug_file, debug_id)
);

-- The eviction scan walks 'ok' rows oldest-use first.
CREATE INDEX app_remote_symbols_lru ON app_remote_symbols (last_used_at) WHERE state = 'ok';
