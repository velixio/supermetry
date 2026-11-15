-- Per-issue daily counts, bucketed in the site's time zone and recomputed
-- idempotently from app_events (internal/approllup).
CREATE TABLE app_issue_daily (
    site_id  integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    issue_id bigint  NOT NULL REFERENCES app_issues(id) ON DELETE CASCADE,
    day      date    NOT NULL,
    events   bigint  NOT NULL DEFAULT 0,
    installs integer NOT NULL DEFAULT 0,
    crashes  bigint  NOT NULL DEFAULT 0,
    PRIMARY KEY (site_id, issue_id, day)
);
