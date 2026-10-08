CREATE TABLE profile_tombstones (
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    external_hash bytea NOT NULL CHECK (octet_length(external_hash) = 32),
    erased_at timestamptz NOT NULL,
    PRIMARY KEY (site_id, external_hash)
);

CREATE INDEX profile_tombstones_erased_at_idx ON profile_tombstones (erased_at);

ALTER TABLE imports
    ADD COLUMN file_hash bytea CHECK (file_hash IS NULL OR octet_length(file_hash) = 32),
    ADD COLUMN replace_existing boolean NOT NULL DEFAULT false;

CREATE INDEX imports_site_file_hash_idx
    ON imports (site_id, file_hash)
    WHERE file_hash IS NOT NULL;
