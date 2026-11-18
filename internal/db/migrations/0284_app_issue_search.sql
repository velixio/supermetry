-- Search over app issues: a per-issue tag rollup the query bar filters on, and
-- saved views (a named search, private to its owner or shared with the site).

-- One row per issue, tag key and value: how many events carried it and when
-- one last did. Maintained by internal/approllup, which recomputes the rows of
-- every issue that had events on the day it recomputes. No user text beyond
-- the bounded dimension values the ingest already stores.
CREATE TABLE app_issue_tags (
    site_id   integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    issue_id  bigint NOT NULL REFERENCES app_issues(id) ON DELETE CASCADE,
    key       text NOT NULL,
    value     text NOT NULL,
    events    bigint NOT NULL,
    last_seen timestamptz NOT NULL,
    PRIMARY KEY (issue_id, key, value)
);
CREATE INDEX app_issue_tags_site_idx ON app_issue_tags (site_id);

CREATE TABLE app_saved_views (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id       integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    -- NULL once the owner is deleted: a private view is then unreachable.
    owner_user_id bigint NULL REFERENCES users(id) ON DELETE SET NULL,
    name          text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    query         text NOT NULL CHECK (char_length(query) <= 200),
    shared        boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (site_id, owner_user_id, name)
);
CREATE INDEX app_saved_views_owner_idx ON app_saved_views (owner_user_id) WHERE owner_user_id IS NOT NULL;
