-- Debug images that events needed and no uploaded file covered, one row per
-- project, kind and debug id, fed by a batched in-memory recorder. The
-- missing-symbols banner and the Symbols settings page read it; uploading the
-- matching file deletes the row, and retention drops rows idle for 30 days.
CREATE TABLE app_missing_debug_files (
    site_id    integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    kind       text    NOT NULL CHECK (char_length(kind) <= 32),
    debug_id   text    NOT NULL CHECK (char_length(debug_id) <= 64),
    -- scrubbed basename of the image's code_file
    code_file  text    NOT NULL DEFAULT '' CHECK (char_length(code_file) <= 256),
    arch       text    NOT NULL DEFAULT '' CHECK (char_length(arch) <= 32),
    first_seen timestamptz NOT NULL,
    last_seen  timestamptz NOT NULL,
    events     bigint  NOT NULL DEFAULT 0 CHECK (events >= 0),
    PRIMARY KEY (site_id, kind, debug_id)
);
