-- Hourly metric rollups, keyed by the site-local wall-clock hour (timestamp
-- without time zone), like span_hourly: Go computes the UTC bounds of each
-- local hour, so +05:30 and +05:45 zones and DST need no time-zone data in
-- Postgres and a local day is a whole number of rows. Monthly partitions are
-- created by the rollup job (db.EnsureMonthlyPartitions) and dropped by
-- retention 90 days (+1 day) after their month ends. Rows are replaced whole
-- (delete then insert of one site-hour), so a rollup is idempotent.
-- Gauge / up-down series: samples, value_sum, value_min, value_max, value_last.
-- Counter series: value_sum of the stored deltas (value_min/max the extremes).
-- Histogram series: count, sum, bucket_counts (element-wise sums, bounds are
-- those of metric_series.bounds), value_min / value_max the min and max seen.
CREATE TABLE metric_hourly (
    site_id       integer          NOT NULL,
    series_id     bigint           NOT NULL,
    hour          timestamp        NOT NULL,
    samples       bigint           NOT NULL,
    value_sum     double precision NULL,
    value_min     double precision NULL,
    value_max     double precision NULL,
    value_last    double precision NULL,
    count         bigint           NULL,
    sum           double precision NULL,
    bucket_counts bigint[]         NULL,
    PRIMARY KEY (series_id, hour)
) PARTITION BY RANGE (hour);

CREATE INDEX metric_hourly_hour_brin ON metric_hourly USING brin (hour);
-- Site deletion and erasure delete by site and hour.
CREATE INDEX metric_hourly_site_hour_idx ON metric_hourly (site_id, hour);
