-- Per-project ownership rules (route new issues to a member) and grouping
-- rules (force a fingerprint). Both are ordered lists replaced as a whole by
-- the settings API; the site registry compiles them for the ingest path.
CREATE TABLE app_ownership_rules (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id       integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    position      integer NOT NULL CHECK (position >= 0),
    matcher       text NOT NULL CHECK (char_length(matcher) BETWEEN 1 AND 70),
    value         text NOT NULL CHECK (char_length(value) BETWEEN 1 AND 200),
    owner_user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX app_ownership_rules_site_idx ON app_ownership_rules (site_id, position);
-- The user FK cascade needs this.
CREATE INDEX app_ownership_rules_owner_idx ON app_ownership_rules (owner_user_id);

CREATE TABLE app_grouping_rules (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id     integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    position    integer NOT NULL CHECK (position >= 0),
    matcher     text NOT NULL CHECK (char_length(matcher) BETWEEN 1 AND 70),
    value       text NOT NULL CHECK (char_length(value) BETWEEN 1 AND 200),
    fingerprint text NOT NULL CHECK (char_length(fingerprint) BETWEEN 1 AND 64)
);
CREATE INDEX app_grouping_rules_site_idx ON app_grouping_rules (site_id, position);
