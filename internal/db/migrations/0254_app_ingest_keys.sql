CREATE TABLE app_ingest_keys (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id    integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    prefix     text NOT NULL CHECK (char_length(prefix) = 8),
    hash       bytea NOT NULL UNIQUE CHECK (octet_length(hash) = 32),
    created_by bigint NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NULL
);
CREATE INDEX app_ingest_keys_site_id_idx ON app_ingest_keys (site_id);
