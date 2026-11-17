-- Uploaded JavaScript source map bundles (Sentry artifact bundles), their
-- indexed files, and the releases and deploys the upload tools create.
-- Blobs live in the shared blob store under bundles/<org id>/<checksum>/.
CREATE TABLE app_artifact_bundles (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id     bigint  NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    -- Empty means every app project of the organization.
    site_ids   integer[] NOT NULL DEFAULT '{}',
    release    text    NOT NULL DEFAULT '',
    dist       text    NOT NULL DEFAULT '',
    -- Chunk checksums in order; assembly reads them back, and a sweep uses them
    -- to find chunk blobs no bundle references any more.
    chunks     text[]  NOT NULL DEFAULT '{}',
    checksum   text    NOT NULL CHECK (checksum ~ '^[0-9a-f]{40}$'),
    state      text    NOT NULL DEFAULT 'created' CHECK (state IN ('created', 'assembling', 'ok', 'error')),
    detail     text,
    size       bigint  NOT NULL DEFAULT 0 CHECK (size >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, checksum)
);

-- One row per file and project (site_id NULL: every project of the org), so a
-- lookup is a single index probe without unnesting the bundle's site list.
CREATE TABLE app_artifact_files (
    bundle_id     bigint  NOT NULL REFERENCES app_artifact_bundles(id) ON DELETE CASCADE,
    org_id        bigint  NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    site_id       integer NULL REFERENCES sites(id) ON DELETE CASCADE,
    debug_id      uuid    NULL,
    release       text    NOT NULL DEFAULT '',
    dist          text    NOT NULL DEFAULT '',
    url           text    NOT NULL,
    type          text    NOT NULL,
    blob_key      text    NOT NULL,
    sourcemap_ref text    NOT NULL DEFAULT '',
    size          bigint  NOT NULL DEFAULT 0 CHECK (size >= 0)
);
CREATE INDEX app_artifact_files_debug_id ON app_artifact_files (org_id, debug_id, bundle_id DESC) WHERE debug_id IS NOT NULL;
CREATE INDEX app_artifact_files_release_url ON app_artifact_files (org_id, release, dist, url, bundle_id DESC) WHERE release <> '';
CREATE INDEX app_artifact_files_bundle ON app_artifact_files (bundle_id);
CREATE INDEX app_artifact_files_site ON app_artifact_files (site_id) WHERE site_id IS NOT NULL;

CREATE TABLE app_releases (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id        bigint  NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    version       text    NOT NULL CHECK (version <> '' AND char_length(version) <= 200),
    site_ids      integer[] NOT NULL DEFAULT '{}',
    url           text    NOT NULL DEFAULT '',
    date_created  timestamptz NOT NULL DEFAULT now(),
    date_released timestamptz NULL,
    UNIQUE (org_id, version)
);

CREATE TABLE app_deploys (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    release_id    bigint  NOT NULL REFERENCES app_releases(id) ON DELETE CASCADE,
    environment   text    NOT NULL CHECK (environment <> '' AND char_length(environment) <= 64),
    name          text    NOT NULL DEFAULT '',
    url           text    NOT NULL DEFAULT '',
    date_started  timestamptz NULL,
    date_finished timestamptz NULL,
    date_created  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX app_deploys_release ON app_deploys (release_id);
