CREATE TABLE segments (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    filters jsonb NOT NULL CHECK (jsonb_typeof(filters) = 'array'),
    created_by bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX segments_site_id_name_lower_key ON segments (site_id, lower(name));
CREATE INDEX segments_created_by_idx ON segments (created_by);
