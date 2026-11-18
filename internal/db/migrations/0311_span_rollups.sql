-- Hourly span rollups. span_hourly is keyed by the site-local wall-clock hour
-- (timestamp without time zone), so +05:30 and +05:45 zones and DST changes
-- need no time-zone data in Postgres: Go computes the UTC bounds of each local
-- hour. The duration histogram is two parallel arrays (bucket, count) over the
-- fixed log-scale buckets of spans.Bucket. Segment rows have is_segment true,
-- group_hash 0 and name = transaction; child rows have is_segment false and
-- name '' (grouped across transactions to bound cardinality); app start rows
-- have is_segment false, name = transaction and op app.start.cold/warm.
CREATE TABLE span_groups (
    site_id     integer     NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    group_hash  bigint      NOT NULL,
    op          text        NOT NULL,
    description text        NOT NULL,
    first_seen  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (site_id, group_hash)
);

CREATE TABLE span_hourly (
    site_id       integer   NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    local_hour    timestamp NOT NULL,
    is_segment    boolean   NOT NULL,
    name          text      NOT NULL,
    op            text      NOT NULL,
    group_hash    bigint    NOT NULL,
    release       text      NOT NULL,
    environment   text      NOT NULL,
    platform      text      NOT NULL,
    count         bigint    NOT NULL,
    errors        bigint    NOT NULL,
    sum_us        bigint    NOT NULL,
    buckets       smallint[] NOT NULL,
    bucket_counts bigint[]  NOT NULL,
    PRIMARY KEY (site_id, local_hour, is_segment, name, op, group_hash, release, environment, platform)
);

CREATE TABLE span_hour_state (
    site_id    integer     NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    local_hour timestamp   NOT NULL,
    status     text        NOT NULL CHECK (status IN ('done', 'dirty')),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (site_id, local_hour)
);
