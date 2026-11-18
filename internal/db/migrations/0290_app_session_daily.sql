-- Daily release-health counts rebuilt from app_sessions and from the
-- pre-counted Sentry session aggregates in app_events. Days are site-local.
-- Abnormal sessions (ANR, hang, watchdog) are counted on their own and are not
-- crashes: crash-free sessions = 1 - crashed / sessions and crash-free users =
-- 1 - crashed_users / users. Aggregates add sessions but carry no install, so
-- they add no users.
CREATE TABLE app_session_daily (
    site_id       integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    day           date    NOT NULL,
    release       text    NOT NULL,
    environment   text    NOT NULL,
    platform      text    NOT NULL,
    sessions      bigint  NOT NULL DEFAULT 0,
    errored       bigint  NOT NULL DEFAULT 0,
    crashed       bigint  NOT NULL DEFAULT 0,
    abnormal      bigint  NOT NULL DEFAULT 0,
    users         integer NOT NULL DEFAULT 0,
    crashed_users integer NOT NULL DEFAULT 0,
    errored_users integer NOT NULL DEFAULT 0,
    PRIMARY KEY (site_id, day, release, environment, platform)
);
