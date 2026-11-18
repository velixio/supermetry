-- Metric points, partitioned daily by ts. Partitions are created ahead of time
-- by the partitions job (db.EnsureDailyPartitions), never on the write path,
-- and dropped by retention (14 days). No foreign key on series_id: the table
-- is append-only and loaded with CopyFrom; site deletion removes rows by
-- site_id (retention.Erase) and the series rows by cascade.
-- Counters and histograms hold per-interval deltas (converted in the writer);
-- gauges and up-down counters hold the sampled value.
CREATE TABLE metric_points (
    site_id       integer          NOT NULL,
    series_id     bigint           NOT NULL,
    ts            timestamptz      NOT NULL,
    value         double precision NULL,
    count         bigint           NULL,
    sum           double precision NULL,
    min           double precision NULL,
    max           double precision NULL,
    bucket_counts bigint[]         NULL
) PARTITION BY RANGE (ts);

CREATE INDEX metric_points_ts_brin ON metric_points USING brin (ts) WITH (pages_per_range = 32, autosummarize = on);
CREATE INDEX metric_points_series_idx ON metric_points (series_id, ts);
