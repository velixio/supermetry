-- Scrubbed full Sentry event bodies, one per stored event, monthly partitions
-- like app_events. Retention is a fixed 30 days by partition drop.
CREATE TABLE app_event_bodies (
    site_id  integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    ts       timestamptz NOT NULL,
    event_id text NOT NULL,
    body     jsonb NOT NULL,
    size     integer NOT NULL DEFAULT 0 CHECK (size >= 0),
    PRIMARY KEY (site_id, event_id, ts)
) PARTITION BY RANGE (ts);

CREATE INDEX app_event_bodies_ts_brin ON app_event_bodies USING brin (ts) WITH (pages_per_range = 8, autosummarize = on);
