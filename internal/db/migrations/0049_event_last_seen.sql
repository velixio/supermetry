-- A compact projection keeps tracker health off the write-heavy events table.
-- Existing installs are populated lazily from retained hot events so startup
-- never scans or takes a write-blocking index-build lock on events.
CREATE TABLE site_last_event (
    site_id bigint PRIMARY KEY REFERENCES sites(id) ON DELETE CASCADE,
    last_event_at timestamptz NOT NULL
);
