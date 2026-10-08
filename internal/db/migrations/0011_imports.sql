CREATE TABLE imports (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    source text NOT NULL CHECK (source IN ('plausible', 'umami')),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    file_name text NOT NULL,
    date_from date,
    date_to date,
    rows bigint NOT NULL DEFAULT 0 CHECK (rows >= 0),
    error text,
    created_by bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK (date_from IS NULL OR date_to IS NULL OR date_from <= date_to)
);

CREATE INDEX imports_site_id_created_at_idx ON imports (site_id, created_at DESC);

CREATE TABLE imported_day (
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    import_id bigint NOT NULL REFERENCES imports(id) ON DELETE CASCADE,
    day date NOT NULL,
    visitors bigint NOT NULL DEFAULT 0 CHECK (visitors >= 0),
    visits bigint NOT NULL DEFAULT 0 CHECK (visits >= 0),
    pageviews bigint NOT NULL DEFAULT 0 CHECK (pageviews >= 0),
    bounces bigint NOT NULL DEFAULT 0 CHECK (bounces >= 0),
    visit_duration_s bigint NOT NULL DEFAULT 0 CHECK (visit_duration_s >= 0),
    PRIMARY KEY (site_id, day, import_id)
);

CREATE INDEX imported_day_import_id_idx ON imported_day (import_id);

CREATE TABLE imported_day_dim (
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    import_id bigint NOT NULL REFERENCES imports(id) ON DELETE CASCADE,
    day date NOT NULL,
    dim smallint NOT NULL CHECK (dim BETWEEN 1 AND 10),
    value text NOT NULL,
    visitors bigint NOT NULL DEFAULT 0 CHECK (visitors >= 0),
    visits bigint NOT NULL DEFAULT 0 CHECK (visits >= 0),
    pageviews bigint NOT NULL DEFAULT 0 CHECK (pageviews >= 0),
    bounces bigint NOT NULL DEFAULT 0 CHECK (bounces >= 0),
    visit_duration_s bigint NOT NULL DEFAULT 0 CHECK (visit_duration_s >= 0),
    events bigint NOT NULL DEFAULT 0 CHECK (events >= 0),
    PRIMARY KEY (site_id, day, dim, value, import_id)
);

CREATE INDEX imported_day_dim_import_id_idx ON imported_day_dim (import_id);
