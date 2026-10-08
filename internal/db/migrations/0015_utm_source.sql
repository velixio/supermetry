ALTER TABLE events
    ADD COLUMN utm_source_id integer NOT NULL DEFAULT 0;

ALTER TABLE sessions
    ADD COLUMN utm_source_id integer NOT NULL DEFAULT 0;

ALTER TABLE sessions_closed
    ADD COLUMN utm_source_id integer NOT NULL DEFAULT 0;

ALTER TABLE custom_closed
    ADD COLUMN utm_source_id integer NOT NULL DEFAULT 0;
