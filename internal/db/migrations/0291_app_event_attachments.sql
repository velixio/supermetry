-- Scrubbed attachment content (images and text) of stored app events, monthly
-- partitions like app_event_bodies. Retention is the bodies' fixed 30 days by
-- partition drop. Content is stored inline: files are capped at 1 MiB (5 MiB
-- per event) at ingest, so TOAST holds them without a second store.
CREATE TABLE app_event_attachments (
    site_id      integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    ts           timestamptz NOT NULL,
    event_id     text NOT NULL,
    idx          smallint NOT NULL CHECK (idx >= 0),
    name         text NOT NULL,
    content_type text NOT NULL,
    size         integer NOT NULL CHECK (size >= 0),
    data         bytea NOT NULL,
    PRIMARY KEY (site_id, event_id, idx, ts)
) PARTITION BY RANGE (ts);

CREATE INDEX app_event_attachments_ts_brin ON app_event_attachments USING brin (ts) WITH (pages_per_range = 8, autosummarize = on);
