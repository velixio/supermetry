-- Per release and platform daily health counts, in the site's time zone.
-- Distinct counts are exact within a day and cannot be summed across days.
CREATE TABLE app_release_daily (
    site_id          integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    day              date    NOT NULL,
    release          text    NOT NULL,
    platform         text    NOT NULL,
    events           bigint  NOT NULL DEFAULT 0,
    crashes          bigint  NOT NULL DEFAULT 0,
    installs         integer NOT NULL DEFAULT 0,
    crashed_installs integer NOT NULL DEFAULT 0,
    sessions         integer NOT NULL DEFAULT 0,
    crashed_sessions integer NOT NULL DEFAULT 0,
    PRIMARY KEY (site_id, day, release, platform)
);
