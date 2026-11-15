-- Per-issue hourly counts on UTC hours, read by the spike alerts.
CREATE TABLE app_issue_hourly (
    site_id  integer     NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    issue_id bigint      NOT NULL REFERENCES app_issues(id) ON DELETE CASCADE,
    hour     timestamptz NOT NULL,
    events   bigint      NOT NULL DEFAULT 0,
    PRIMARY KEY (site_id, issue_id, hour)
);
