-- Release API completion: the ref, start date and open/archived status that
-- sentry-cli and the bundler plugins send, and the index behind the per-release
-- "new issues" counter (app_issues had only UNIQUE (site_id, fingerprint)).
ALTER TABLE app_releases
    ADD COLUMN ref          text NOT NULL DEFAULT '' CHECK (char_length(ref) <= 200),
    ADD COLUMN date_started timestamptz NULL,
    ADD COLUMN status       text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'archived'));

CREATE INDEX app_issues_site_first_release ON app_issues (site_id, first_release);
