-- first_release and last_release already exist (0255); the writer keeps level
-- and culprit from the newest event.
ALTER TABLE app_issues
    ADD COLUMN level            text NOT NULL DEFAULT '',
    ADD COLUMN culprit          text NOT NULL DEFAULT '',
    ADD COLUMN grouping_version smallint NOT NULL DEFAULT 1;
