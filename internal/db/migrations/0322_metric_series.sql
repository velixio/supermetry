-- Metric series registry: one row per distinct (site, name, kind, unit, bucket
-- bounds, attributes). attrs_hash is sha256 over those fields (computed in Go,
-- obsmetrics.SeriesKey.Hash), so a histogram whose bucket bounds change becomes
-- a new series instead of merging incompatible buckets. The writer inserts
-- unknown series in batches and touches last_seen at most once an hour.
CREATE TABLE metric_series (
    id         bigint           GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id    integer          NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    name       text             NOT NULL,
    kind       text             NOT NULL CHECK (kind IN ('gauge', 'counter', 'updown', 'histogram')),
    unit       text             NOT NULL DEFAULT '',
    bounds     double precision[] NULL,
    attrs      jsonb            NOT NULL DEFAULT '{}',
    attrs_hash bytea            NOT NULL,
    first_seen timestamptz      NOT NULL,
    last_seen  timestamptz      NOT NULL,
    UNIQUE (site_id, attrs_hash)
);

CREATE INDEX metric_series_name_idx ON metric_series (site_id, name);
