-- Daily intake counters per app site and class (logs, metrics). The intake
-- keeps the running totals in memory and flushes the deltas every minute; the
-- Health page and Cloud metering read the table.
CREATE TABLE obs_ingest_daily (
    site_id  integer NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    day      date    NOT NULL,
    class    text    NOT NULL CHECK (class IN ('logs', 'metrics')),
    accepted bigint  NOT NULL DEFAULT 0,
    rejected bigint  NOT NULL DEFAULT 0,
    bytes    bigint  NOT NULL DEFAULT 0,
    PRIMARY KEY (site_id, day, class)
);
