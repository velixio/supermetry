-- Application logs: OTLP log records and Sentry log items, one row per record,
-- partitioned daily by ts (7-day default retention makes monthly partitions
-- useless for dropping). Partitions are created ahead of time by the
-- partitions job (db.EnsureDailyPartitions), never on the write path, and
-- dropped by retention.
-- There is no primary key: rows have no natural key, and duplicates from SDK
-- retries are tolerated. rid is a writer-assigned random value that only
-- gives rows with the same ts a stable keyset order (identity columns on
-- partitioned tables need PG 17). user_hash is the keyed hash of the user id,
-- never the id; attributes never hold IPs or emails (scrubbed at intake).
CREATE TABLE logs (
    site_id     integer     NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    ts          timestamptz NOT NULL,
    rid         bigint      NOT NULL,
    observed_at timestamptz NOT NULL,
    severity    smallint    NOT NULL CHECK (severity BETWEEN 0 AND 24),
    service     text        NOT NULL DEFAULT '',
    environment text        NOT NULL DEFAULT '',
    release     text        NOT NULL DEFAULT '',
    trace_id    bytea       NULL CHECK (octet_length(trace_id) = 16),
    span_id     bytea       NULL CHECK (octet_length(span_id) = 8),
    user_hash   bytea       NULL,
    body        text        NOT NULL,
    truncated   boolean     NOT NULL DEFAULT false,
    attributes  jsonb       NOT NULL DEFAULT '{}',
    source      text        NOT NULL CHECK (source IN ('otlp', 'sentry'))
) PARTITION BY RANGE (ts);

CREATE INDEX logs_ts_brin ON logs USING brin (ts) WITH (pages_per_range = 32, autosummarize = on);
-- Every query is site + time range, and this is the keyset paging order.
CREATE INDEX logs_site_ts_idx ON logs (site_id, ts DESC, rid DESC);
CREATE INDEX logs_trace_idx ON logs (trace_id) WHERE trace_id IS NOT NULL;
