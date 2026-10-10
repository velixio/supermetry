ALTER TABLE dict
    ADD COLUMN first_site_id integer,
    ADD COLUMN created_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN gc_marked_at timestamptz;

CREATE INDEX dict_kind_first_site_idx
    ON dict (kind, first_site_id)
    WHERE first_site_id IS NOT NULL;

CREATE INDEX dict_gc_marked_idx
    ON dict (gc_marked_at, kind, id)
    WHERE gc_marked_at IS NOT NULL;
