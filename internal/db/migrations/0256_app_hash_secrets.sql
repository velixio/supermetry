CREATE TABLE app_hash_secrets (
    site_id    integer PRIMARY KEY REFERENCES sites(id) ON DELETE CASCADE,
    secret     bytea NOT NULL CHECK (octet_length(secret) = 32),
    created_at timestamptz NOT NULL DEFAULT now()
);
