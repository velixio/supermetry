-- Debug files (dSYM, ELF, ProGuard mappings, ...) uploaded through the Sentry
-- compatible difs/assemble endpoint. The raw file is assembled from chunks and
-- kept in the shared blob store under difs/<org id>/<checksum>; this row
-- records the upload, and chunks keeps the chunk blobs referenced until swept.
CREATE TABLE app_dif_uploads (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id           bigint  NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    -- Empty means every app project of the organization.
    site_ids         integer[] NOT NULL DEFAULT '{}',
    checksum         text    NOT NULL CHECK (checksum ~ '^[0-9a-f]{40}$'),
    -- The upload name as sent by the client, clipped; never trusted as a path.
    name             text    NOT NULL DEFAULT '' CHECK (char_length(name) <= 1024),
    declared_debug_id text   NULL CHECK (char_length(declared_debug_id) <= 64),
    chunks           text[]  NOT NULL DEFAULT '{}',
    state            text    NOT NULL DEFAULT 'created' CHECK (state IN ('created', 'assembling', 'ok', 'error')),
    detail           text    NULL CHECK (char_length(detail) <= 300),
    size             bigint  NOT NULL DEFAULT 0 CHECK (size >= 0),
    blob_key         text    NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, checksum)
);
