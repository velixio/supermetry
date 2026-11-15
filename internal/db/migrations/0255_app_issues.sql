CREATE TABLE app_issues (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id             integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    fingerprint         text NOT NULL CHECK (char_length(fingerprint) BETWEEN 1 AND 64),
    kind                text NOT NULL,
    error_type          text NOT NULL DEFAULT '',
    title               text NOT NULL DEFAULT '',
    first_seen          timestamptz NOT NULL,
    last_seen           timestamptz NOT NULL,
    first_release       text NOT NULL DEFAULT '',
    last_release        text NOT NULL DEFAULT '',
    status              text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'ignored', 'regressed')),
    resolved_in_release text NULL,
    event_count         bigint NOT NULL DEFAULT 0,
    UNIQUE (site_id, fingerprint)
);
