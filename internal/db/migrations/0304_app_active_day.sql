-- One row per app install per site-local day: the unit behind active users
-- (DAU/WAU/MAU), stickiness, adoption splits and engagement. Rebuilt per day
-- by approllup.RecomputeDay from events, so it is idempotent and a late report
-- simply recomputes its day. visitor_id is the keyed install hash, never a
-- raw install id. release and platform are the install's last values that day
-- (platform is the hostname dictionary value). events counts custom events,
-- screens counts screen views.
CREATE TABLE app_active_day (
    site_id integer NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    day date NOT NULL,
    visitor_id bigint NOT NULL,
    release text NOT NULL DEFAULT '',
    platform text NOT NULL DEFAULT '',
    country character(2) NOT NULL DEFAULT '',
    os_id integer NOT NULL DEFAULT 0,
    device smallint NOT NULL DEFAULT 0,
    events integer NOT NULL,
    screens integer NOT NULL,
    sessions integer NOT NULL,
    engagement_ms bigint NOT NULL,
    PRIMARY KEY (site_id, day, visitor_id)
) PARTITION BY RANGE (day);

CREATE INDEX app_active_day_brin ON app_active_day
    USING brin (day)
    WITH (pages_per_range = 8, autosummarize = on);

-- "Is this install new on its first day" and install erasure look an install
-- up across days; the primary key leads with day and cannot serve that.
CREATE INDEX app_active_day_visitor_idx ON app_active_day (site_id, visitor_id, day);
