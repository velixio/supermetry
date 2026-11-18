-- Workflow notifications: per-user, per-project toggles, and a delivery mark on
-- the activity rows so the notifier sends each event once and retries failures.
CREATE TABLE user_notification_prefs (
    user_id   bigint  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    site_id   integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    assigned  boolean NOT NULL DEFAULT true,
    resolved  boolean NOT NULL DEFAULT false,
    regressed boolean NOT NULL DEFAULT true,
    PRIMARY KEY (user_id, site_id)
);
-- The site_id FK cascade needs this when a project is deleted.
CREATE INDEX user_notification_prefs_site_idx ON user_notification_prefs (site_id);

ALTER TABLE app_issue_activity ADD COLUMN notified_at timestamptz NULL;
-- Activity that exists now predates the notifier and is never mailed.
UPDATE app_issue_activity SET notified_at = now();
CREATE INDEX app_issue_activity_unnotified_idx ON app_issue_activity (id)
    WHERE notified_at IS NULL AND kind IN ('assigned', 'status', 'regressed');
