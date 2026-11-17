-- Named groups of product event names that count as one feature for adoption
-- reporting (an app project defines "checkout" as purchase + paywall_accept).
-- Only event names are stored, never user or install data. Deleting the
-- project removes its sets.
CREATE TABLE app_feature_sets (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    event_names text[] NOT NULL
        CHECK (cardinality(event_names) BETWEEN 1 AND 20),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (site_id, name)
);
