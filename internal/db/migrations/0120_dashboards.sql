-- Saved dashboards. layout is a versioned JSON document of widgets (kinds that
-- map to existing query capabilities); it never holds SQL. version supports
-- optimistic concurrency. At most one dashboard per site is the default.
CREATE TABLE dashboards (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    layout jsonb NOT NULL CHECK (jsonb_typeof(layout) = 'object'),
    is_default boolean NOT NULL DEFAULT false,
    version integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_by bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX dashboards_site_id_name_lower_key ON dashboards (site_id, lower(name));
CREATE UNIQUE INDEX dashboards_site_default_key ON dashboards (site_id) WHERE is_default;
CREATE INDEX dashboards_created_by_idx ON dashboards (created_by);
