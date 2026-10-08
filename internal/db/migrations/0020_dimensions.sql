-- G-D3: bounded visitor dimensions. Old rows keep 0 (Unknown); no backfill.
ALTER TABLE events
    ADD COLUMN browser_ver_id integer NOT NULL DEFAULT 0,
    ADD COLUMN os_ver_id integer NOT NULL DEFAULT 0,
    ADD COLUMN language_id integer NOT NULL DEFAULT 0,
    ADD COLUMN hostname_id integer NOT NULL DEFAULT 0;

ALTER TABLE sessions
    ADD COLUMN browser_ver_id integer NOT NULL DEFAULT 0,
    ADD COLUMN os_ver_id integer NOT NULL DEFAULT 0,
    ADD COLUMN language_id integer NOT NULL DEFAULT 0,
    ADD COLUMN hostname_id integer NOT NULL DEFAULT 0,
    ADD COLUMN screen_bucket smallint NOT NULL DEFAULT 0;

ALTER TABLE sessions_closed
    ADD COLUMN browser_ver_id integer NOT NULL DEFAULT 0,
    ADD COLUMN os_ver_id integer NOT NULL DEFAULT 0,
    ADD COLUMN language_id integer NOT NULL DEFAULT 0,
    ADD COLUMN hostname_id integer NOT NULL DEFAULT 0,
    ADD COLUMN screen_bucket smallint NOT NULL DEFAULT 0;

ALTER TABLE custom_closed
    ADD COLUMN browser_ver_id integer NOT NULL DEFAULT 0,
    ADD COLUMN os_ver_id integer NOT NULL DEFAULT 0,
    ADD COLUMN language_id integer NOT NULL DEFAULT 0,
    ADD COLUMN hostname_id integer NOT NULL DEFAULT 0,
    ADD COLUMN screen_bucket smallint NOT NULL DEFAULT 0;
