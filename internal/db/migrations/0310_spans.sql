-- Performance spans: Sentry transactions and spans and OTLP traces, one row per
-- span, partitioned daily by start_ts. Partitions are created ahead of time by
-- the partitions job (db.EnsureDailyPartitions), never on the write path, and
-- dropped by retention. No primary key and no foreign key: the table is
-- append-only and high volume, and site deletion uses retention.Erase.
-- bucket is the log-scale duration bucket computed in Go at ingest
-- (spans.Bucket), so SQL never recomputes it.
CREATE TABLE spans (
    site_id          integer     NOT NULL,
    start_ts         timestamptz NOT NULL,
    duration_us      bigint      NOT NULL CHECK (duration_us >= 0),
    bucket           smallint    NOT NULL,
    trace_id         bytea       NOT NULL CHECK (octet_length(trace_id) = 16),
    span_id          bytea       NOT NULL CHECK (octet_length(span_id) = 8),
    parent_span_id   bytea       NULL,
    segment_id       bytea       NOT NULL,
    is_segment       boolean     NOT NULL,
    name             text        NOT NULL,
    op               text        NOT NULL DEFAULT '',
    description      text        NOT NULL DEFAULT '',
    group_hash       bigint      NOT NULL DEFAULT 0,
    status           text        NOT NULL DEFAULT '',
    http_status      smallint    NULL,
    release          text        NOT NULL DEFAULT '',
    environment      text        NOT NULL DEFAULT '',
    platform         text        NOT NULL DEFAULT '',
    app_start_type   text        NULL CHECK (app_start_type IN ('cold', 'warm')),
    app_start_ms     integer     NULL,
    app_start_bucket smallint    NULL,
    attrs            jsonb       NULL
) PARTITION BY RANGE (start_ts);

CREATE INDEX spans_start_brin ON spans USING brin (start_ts) WITH (pages_per_range = 32, autosummarize = on);
CREATE INDEX spans_trace_idx ON spans (site_id, trace_id);
CREATE INDEX spans_segment_idx ON spans (site_id, name, start_ts) WHERE is_segment;
