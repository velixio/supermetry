-- DSN keys: the public half of a Sentry-style DSN, shown in client bundles.
-- They live beside the hashed native keys. hash is sha256(public_key) so the
-- existing NOT NULL UNIQUE contract holds; prefix is its first 8 characters.
ALTER TABLE app_ingest_keys
    ADD COLUMN kind               text NOT NULL DEFAULT 'smk' CHECK (kind IN ('smk', 'dsn')),
    ADD COLUMN public_key         text NULL UNIQUE CHECK (public_key ~ '^[0-9a-f]{32}$'),
    ADD COLUMN allowed_origins    text[] NOT NULL DEFAULT '{}',
    ADD COLUMN label              text NOT NULL DEFAULT '',
    ADD COLUMN rate_limit_per_min integer NULL CHECK (rate_limit_per_min > 0),
    ADD COLUMN revoked_at         timestamptz NULL,
    ADD COLUMN last_used_at       timestamptz NULL,
    ADD CONSTRAINT app_ingest_keys_dsn_public_key CHECK ((kind = 'dsn') = (public_key IS NOT NULL));
