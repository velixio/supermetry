CREATE TABLE sessions_closed (
    site_id integer NOT NULL,
    day date NOT NULL,
    visitor_id bigint NOT NULL,
    session_id bigint NOT NULL,
    source_id integer NOT NULL DEFAULT 0,
    medium_id integer NOT NULL DEFAULT 0,
    campaign_id integer NOT NULL DEFAULT 0,
    utm_content_id integer NOT NULL DEFAULT 0,
    utm_term_id integer NOT NULL DEFAULT 0,
    country character(2) NOT NULL DEFAULT '',
    browser_id integer NOT NULL DEFAULT 0,
    os_id integer NOT NULL DEFAULT 0,
    device smallint NOT NULL DEFAULT 0,
    entry_path integer NOT NULL DEFAULT 0,
    exit_path integer NOT NULL DEFAULT 0,
    pageviews integer NOT NULL,
    custom integer NOT NULL,
    duration_ms integer NOT NULL,
    engagement_ms bigint NOT NULL,
    vd_n smallint NOT NULL,
    flags smallint NOT NULL,
    seq integer[] NOT NULL,
    seq_engage integer[] NOT NULL,
    seq_scroll smallint[] NOT NULL
) PARTITION BY RANGE (day);

CREATE INDEX sessions_closed_brin ON sessions_closed
    USING brin (site_id, day, country, visitor_id)
    WITH (pages_per_range = 16, autosummarize = on);

CREATE TABLE custom_closed (
    site_id integer NOT NULL,
    day date NOT NULL,
    visitor_id bigint NOT NULL,
    name_id integer NOT NULL,
    path_id integer NOT NULL,
    source_id integer NOT NULL DEFAULT 0,
    medium_id integer NOT NULL DEFAULT 0,
    campaign_id integer NOT NULL DEFAULT 0,
    utm_content_id integer NOT NULL DEFAULT 0,
    utm_term_id integer NOT NULL DEFAULT 0,
    country character(2) NOT NULL DEFAULT '',
    browser_id integer NOT NULL DEFAULT 0,
    os_id integer NOT NULL DEFAULT 0,
    device smallint NOT NULL DEFAULT 0,
    entry_path integer NOT NULL DEFAULT 0,
    exit_path integer NOT NULL DEFAULT 0,
    props jsonb
) PARTITION BY RANGE (day);

CREATE INDEX custom_closed_brin ON custom_closed
    USING brin (site_id, day)
    WITH (pages_per_range = 8, autosummarize = on);

CREATE TABLE cube_day (
    site_id integer NOT NULL,
    day date NOT NULL,
    source_id integer NOT NULL,
    country character(2) NOT NULL,
    browser_id integer NOT NULL,
    os_id integer NOT NULL,
    device smallint NOT NULL,
    v_vd integer NOT NULL,
    v_src integer NOT NULL,
    visits integer NOT NULL,
    pageviews integer NOT NULL,
    bounces integer NOT NULL,
    duration_ms bigint NOT NULL,
    engagement_ms bigint NOT NULL,
    scroll_sum bigint NOT NULL,
    scroll_n bigint NOT NULL
) PARTITION BY RANGE (day);

CREATE INDEX cube_day_brin ON cube_day
    USING brin (site_id, day)
    WITH (pages_per_range = 8, autosummarize = on);

CREATE TABLE visitor_day (
    site_id integer NOT NULL,
    day date NOT NULL,
    country character(2) NOT NULL,
    browser_id integer NOT NULL,
    os_id integer NOT NULL,
    device smallint NOT NULL,
    visitors integer NOT NULL,
    visits integer NOT NULL,
    pageviews integer NOT NULL,
    bounces integer NOT NULL,
    duration_ms bigint NOT NULL,
    engagement_ms bigint NOT NULL
) PARTITION BY RANGE (day);

CREATE INDEX visitor_day_brin ON visitor_day
    USING brin (site_id, day, country)
    WITH (pages_per_range = 8, autosummarize = on);

CREATE TABLE rollup_day (
    site_id integer NOT NULL,
    day date NOT NULL,
    visitors integer NOT NULL,
    visits integer NOT NULL,
    pageviews integer NOT NULL,
    bounces integer NOT NULL,
    duration_ms bigint NOT NULL,
    engagement_ms bigint NOT NULL,
    scroll_sum bigint NOT NULL,
    scroll_n bigint NOT NULL,
    PRIMARY KEY (site_id, day)
) PARTITION BY RANGE (day);

CREATE TABLE rollup_day_dim (
    site_id integer NOT NULL,
    day date NOT NULL,
    dim smallint NOT NULL,
    value_id integer NOT NULL,
    visitors integer NOT NULL,
    visits integer NOT NULL,
    pageviews integer NOT NULL,
    bounces integer NOT NULL,
    duration_ms bigint NOT NULL,
    engagement_ms bigint NOT NULL,
    scroll_sum bigint NOT NULL,
    scroll_n bigint NOT NULL,
    PRIMARY KEY (site_id, day, dim, value_id)
) PARTITION BY RANGE (day);

CREATE TABLE rollup_month_dim (
    site_id integer NOT NULL,
    month date NOT NULL,
    dim smallint NOT NULL,
    value_id integer NOT NULL,
    visitors bigint NOT NULL,
    visits bigint NOT NULL,
    pageviews bigint NOT NULL,
    bounces bigint NOT NULL,
    duration_ms bigint NOT NULL,
    engagement_ms bigint NOT NULL,
    scroll_sum bigint NOT NULL,
    scroll_n bigint NOT NULL,
    PRIMARY KEY (site_id, month, dim, value_id)
);

CREATE TABLE rollup_month_done (
    site_id integer NOT NULL,
    month date NOT NULL,
    PRIMARY KEY (site_id, month)
);

CREATE TABLE rollup_block_dim (
    site_id integer NOT NULL,
    k smallint NOT NULL CHECK (k BETWEEN 1 AND 9),
    start_day date NOT NULL,
    dim smallint NOT NULL,
    value_id integer NOT NULL,
    visitors bigint NOT NULL,
    visits bigint NOT NULL,
    pageviews bigint NOT NULL,
    bounces bigint NOT NULL,
    duration_ms bigint NOT NULL,
    engagement_ms bigint NOT NULL,
    scroll_sum bigint NOT NULL,
    scroll_n bigint NOT NULL,
    PRIMARY KEY (site_id, k, start_day, dim, value_id)
);

CREATE TABLE rollup_block_done (
    site_id integer NOT NULL,
    k smallint NOT NULL CHECK (k BETWEEN 0 AND 9),
    start_day date NOT NULL,
    PRIMARY KEY (site_id, k, start_day)
);
