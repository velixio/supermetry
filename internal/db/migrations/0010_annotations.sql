CREATE TABLE annotations (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    day date NOT NULL,
    text text NOT NULL CHECK (char_length(btrim(text)) BETWEEN 1 AND 200),
    created_by bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX annotations_site_id_day_idx ON annotations (site_id, day);
