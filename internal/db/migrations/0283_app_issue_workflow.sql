-- Issue workflow: assignee, priority, snooze, archive, merge target and a
-- GitHub link on the issue itself; an activity timeline and comments in two
-- new tables. All writes go through internal/appissues.
ALTER TABLE app_issues
    ADD COLUMN assignee_user_id          bigint NULL REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN priority                  text NOT NULL DEFAULT 'medium' CHECK (priority IN ('high', 'medium', 'low')),
    ADD COLUMN snooze_until              timestamptz NULL,
    ADD COLUMN snooze_events             bigint NULL,
    ADD COLUMN snooze_users              bigint NULL,
    ADD COLUMN snooze_base_events        bigint NULL,
    ADD COLUMN snooze_base_users         bigint NULL,
    ADD COLUMN archived_until_escalating boolean NOT NULL DEFAULT false,
    ADD COLUMN merged_into               bigint NULL REFERENCES app_issues(id),
    ADD COLUMN github_url                text NULL;

ALTER TABLE app_issues DROP CONSTRAINT app_issues_status_check;
ALTER TABLE app_issues ADD CONSTRAINT app_issues_status_check
    CHECK (status IN ('open', 'resolved', 'ignored', 'regressed', 'archived'));

-- Deleting a user must not be blocked by, or lose, the issues assigned to them;
-- the FK needs an index for the SET NULL scan.
CREATE INDEX app_issues_assignee_idx ON app_issues (assignee_user_id) WHERE assignee_user_id IS NOT NULL;

-- One row per change on an issue. actor_user_id is an org user, NULL for the
-- system (regressions, first sight) or a user that was deleted since.
CREATE TABLE app_issue_activity (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id       integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    issue_id      bigint NOT NULL REFERENCES app_issues(id) ON DELETE CASCADE,
    at            timestamptz NOT NULL,
    actor_user_id bigint NULL REFERENCES users(id) ON DELETE SET NULL,
    kind          text NOT NULL CHECK (char_length(kind) BETWEEN 1 AND 32),
    data          jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX app_issue_activity_issue_idx ON app_issue_activity (issue_id, at DESC);
-- The site_id FK cascade needs this when a project is deleted.
CREATE INDEX app_issue_activity_site_idx ON app_issue_activity (site_id);

CREATE TABLE app_issue_comments (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id        integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    issue_id       bigint NOT NULL REFERENCES app_issues(id) ON DELETE CASCADE,
    author_user_id bigint NULL REFERENCES users(id) ON DELETE SET NULL,
    body           text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 10000),
    created_at     timestamptz NOT NULL,
    edited_at      timestamptz NULL,
    deleted_at     timestamptz NULL
);
CREATE INDEX app_issue_comments_issue_idx ON app_issue_comments (issue_id, created_at);
CREATE INDEX app_issue_comments_site_idx ON app_issue_comments (site_id);
