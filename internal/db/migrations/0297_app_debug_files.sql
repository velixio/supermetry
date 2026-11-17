-- One row per debug object found in an uploaded file and per target project
-- (site_id NULL: every project of the organization), like app_artifact_files,
-- so a lookup is a single index probe. A fat Mach-O yields one row per slice.
-- cache_* describe the compact symbol cache converted from the raw file.
CREATE TABLE app_debug_files (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    upload_id     bigint  NOT NULL REFERENCES app_dif_uploads(id) ON DELETE CASCADE,
    org_id        bigint  NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    site_id       integer NULL REFERENCES sites(id) ON DELETE CASCADE,
    debug_id      text    NOT NULL CHECK (char_length(debug_id) <= 64),
    code_id       text    NOT NULL DEFAULT '' CHECK (char_length(code_id) <= 128),
    -- breakpad is listed now so a later phase needs no CHECK change.
    kind          text    NOT NULL CHECK (kind IN ('macho', 'elf', 'pe', 'breakpad', 'proguard', 'sourcebundle', 'other')),
    arch          text    NOT NULL DEFAULT '' CHECK (char_length(arch) <= 32),
    object_name   text    NOT NULL DEFAULT '' CHECK (char_length(object_name) <= 1024),
    features      text[]  NOT NULL DEFAULT '{}',
    cache_state   text    NOT NULL DEFAULT 'pending' CHECK (cache_state IN ('pending', 'ok', 'error', 'unsupported')),
    cache_version smallint NOT NULL DEFAULT 0,
    cache_key     text    NULL,
    cache_size    bigint  NOT NULL DEFAULT 0 CHECK (cache_size >= 0),
    detail        text    NULL CHECK (char_length(detail) <= 300),
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX app_debug_files_lookup ON app_debug_files (org_id, kind, debug_id, id DESC);
CREATE INDEX app_debug_files_upload ON app_debug_files (upload_id);
CREATE INDEX app_debug_files_site ON app_debug_files (site_id) WHERE site_id IS NOT NULL;
-- The cache worker's restart scan.
CREATE INDEX app_debug_files_pending ON app_debug_files (id) WHERE cache_state = 'pending';
