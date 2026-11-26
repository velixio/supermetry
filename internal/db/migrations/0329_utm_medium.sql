-- Raw utm_medium as tagged, stored apart from the derived medium_id (which
-- also holds referrer-derived values such as "organic" or "referral" for
-- untagged traffic). Mirrors 0015_utm_source.sql. Rows written before this
-- migration keep utm_medium_id = 0, so the "UTM medium" breakdown shows no
-- value for old data until it is re-ingested.
ALTER TABLE events
    ADD COLUMN utm_medium_id integer NOT NULL DEFAULT 0;

ALTER TABLE sessions
    ADD COLUMN utm_medium_id integer NOT NULL DEFAULT 0;

-- Hot sessions also keep the first hit's utm_content/utm_term so the hot-tier
-- breakdowns need no per-session events lookup. Sessions opened before this
-- migration keep 0 until closed (the closer reads the first event).
ALTER TABLE sessions
    ADD COLUMN utm_content_id integer NOT NULL DEFAULT 0,
    ADD COLUMN utm_term_id integer NOT NULL DEFAULT 0;

ALTER TABLE sessions_closed
    ADD COLUMN utm_medium_id integer NOT NULL DEFAULT 0;

ALTER TABLE custom_closed
    ADD COLUMN utm_medium_id integer NOT NULL DEFAULT 0;

-- custom_closed.event_utm_medium_id (0029) holds the event's DERIVED medium
-- and feeds the event-level channel, so the event's raw tagged medium gets its
-- own column. NULL means the row predates this column.
ALTER TABLE custom_closed
    ADD COLUMN event_raw_utm_medium_id integer;

-- Rollup dimension 17 (utm_medium) used to be keyed by the derived medium_id,
-- so its closed rows mix referrer-derived values into the UTM medium
-- breakdown. Drop them; `supermetry recompute-rollups` rebuilds them from
-- utm_medium_id (empty for data closed before this migration).
DELETE FROM rollup_day_dim WHERE dim = 17;
DELETE FROM rollup_month_dim WHERE dim = 17;
DELETE FROM rollup_block_dim WHERE dim = 17;
