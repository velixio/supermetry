CREATE TABLE tracking_pixels (
    id bigserial PRIMARY KEY,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    tag text NOT NULL CHECK (tag ~ '^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (site_id, tag)
);

CREATE INDEX tracking_pixels_site_id_id_idx ON tracking_pixels (site_id, id DESC);
CREATE INDEX pixel_impressions_daily_site_campaign_day_idx ON pixel_impressions_daily (site_id, campaign, day);

INSERT INTO tracking_pixels (site_id, name, tag)
SELECT DISTINCT site_id, campaign, campaign
FROM pixel_impressions_daily
WHERE campaign <> ''
ON CONFLICT (site_id, tag) DO NOTHING;
