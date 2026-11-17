-- Fingerprint to issue mapping, so grouping upgrades and later merges can
-- point several (version, hash) pairs at one issue. hash is the sha256 of the
-- issue's fingerprint text; the writer records every pair it groups an event by.
CREATE TABLE app_issue_hashes (
    site_id          integer  NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    hash             bytea    NOT NULL CHECK (octet_length(hash) = 32),
    grouping_version smallint NOT NULL,
    issue_id         bigint   NOT NULL REFERENCES app_issues(id) ON DELETE CASCADE,
    PRIMARY KEY (site_id, grouping_version, hash)
);

-- The cascade from app_issues and lookups by issue need this.
CREATE INDEX app_issue_hashes_issue_idx ON app_issue_hashes (issue_id);
