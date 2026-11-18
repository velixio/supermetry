-- Commits behind a release (sentry-cli releases set-commits). Commits are
-- stored once per repository and linked to any number of releases; the
-- changed paths back the later suspect-commit lookup. Author emails are
-- customer-supplied developer metadata: kept here, exported, never sent to MCP.
CREATE TABLE app_repositories (
    id     bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id bigint NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name   text   NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    UNIQUE (org_id, name)
);

CREATE TABLE app_commits (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id        bigint NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    repository_id bigint NOT NULL REFERENCES app_repositories (id) ON DELETE CASCADE,
    sha           text   NOT NULL CHECK (sha ~ '^[0-9a-f]{4,64}$'),
    message       text   NOT NULL DEFAULT '' CHECK (char_length(message) <= 2000),
    author_name   text   NOT NULL DEFAULT '' CHECK (char_length(author_name) <= 200),
    author_email  text   NOT NULL DEFAULT '' CHECK (char_length(author_email) <= 320),
    committed_at  timestamptz NULL,
    UNIQUE (repository_id, sha)
);
CREATE INDEX app_commits_org ON app_commits (org_id);

CREATE TABLE app_commit_files (
    commit_id bigint NOT NULL REFERENCES app_commits (id) ON DELETE CASCADE,
    path      text   NOT NULL CHECK (char_length(path) BETWEEN 1 AND 1024),
    change    text   NOT NULL DEFAULT 'M' CHECK (change IN ('A', 'M', 'D')),
    PRIMARY KEY (commit_id, path)
);

CREATE TABLE app_release_commits (
    release_id bigint  NOT NULL REFERENCES app_releases (id) ON DELETE CASCADE,
    commit_id  bigint  NOT NULL REFERENCES app_commits (id) ON DELETE CASCADE,
    position   integer NOT NULL,
    PRIMARY KEY (release_id, commit_id)
);
CREATE INDEX app_release_commits_commit ON app_release_commits (commit_id);

CREATE TABLE app_release_refs (
    release_id   bigint NOT NULL REFERENCES app_releases (id) ON DELETE CASCADE,
    repository   text   NOT NULL CHECK (char_length(repository) BETWEEN 1 AND 200),
    commit_sha   text   NOT NULL CHECK (commit_sha ~ '^[0-9a-f]{4,64}$'),
    previous_sha text   NULL CHECK (previous_sha IS NULL OR previous_sha ~ '^[0-9a-f]{4,64}$'),
    PRIMARY KEY (release_id, repository)
);
