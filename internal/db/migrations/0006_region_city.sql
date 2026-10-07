ALTER TABLE events
    ADD COLUMN region_id integer NOT NULL DEFAULT 0,
    ADD COLUMN city_id integer NOT NULL DEFAULT 0;

ALTER TABLE sessions
    ADD COLUMN region_id integer NOT NULL DEFAULT 0,
    ADD COLUMN city_id integer NOT NULL DEFAULT 0;

ALTER TABLE sessions_closed
    ADD COLUMN region_id integer NOT NULL DEFAULT 0,
    ADD COLUMN city_id integer NOT NULL DEFAULT 0;

ALTER TABLE custom_closed
    ADD COLUMN region_id integer NOT NULL DEFAULT 0,
    ADD COLUMN city_id integer NOT NULL DEFAULT 0;
