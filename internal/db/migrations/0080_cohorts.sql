-- Saved audience definitions. A cohort stores conditions only; membership is
-- evaluated on demand against retained profiles and events, never persisted.
CREATE TABLE cohorts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    definition jsonb NOT NULL CHECK (jsonb_typeof(definition) = 'object'),
    created_by bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX cohorts_site_id_name_lower_key ON cohorts (site_id, lower(name));
CREATE INDEX cohorts_created_by_idx ON cohorts (created_by);
