-- Pixel analytics projects: a site is either web analytics or pixel analytics.
-- Pixel views are daily aggregates; no IPs or user agents are stored.

ALTER TABLE sites ADD COLUMN kind text NOT NULL DEFAULT 'web' CHECK (kind IN ('web', 'pixel'));

CREATE TABLE pixels (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    public_id text NOT NULL UNIQUE CHECK (public_id ~ '^p[A-Za-z0-9]{11}$'),
    name text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 120),
    badge_label text NOT NULL DEFAULT 'views' CHECK (char_length(badge_label) BETWEEN 1 AND 40),
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX pixels_site_id_idx ON pixels (site_id, id);

CREATE TABLE pixel_views_daily (
    pixel_id bigint NOT NULL REFERENCES pixels(id) ON DELETE CASCADE,
    day date NOT NULL,
    country text NOT NULL DEFAULT '' CHECK (char_length(country) <= 2),
    via text NOT NULL CHECK (via IN ('proxy', 'direct')),
    views bigint NOT NULL CHECK (views > 0),
    PRIMARY KEY (pixel_id, day, country, via)
);
