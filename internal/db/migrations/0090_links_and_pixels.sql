-- Managed short links and tracking pixels.
--
-- Click and impression counts are small daily aggregates keyed by the site's
-- local calendar day (computed at write time in the site's time zone), so they
-- never hold raw IPs, user agents or per-visitor rows.

CREATE TABLE links (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    slug text NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9_-]{1,63}$'),
    name text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 120),
    destination text NOT NULL CHECK (char_length(destination) <= 2048 AND destination ~* '^https?://'),
    utm_source text CHECK (utm_source IS NULL OR char_length(utm_source) <= 120),
    utm_medium text CHECK (utm_medium IS NULL OR char_length(utm_medium) <= 120),
    utm_campaign text CHECK (utm_campaign IS NULL OR char_length(utm_campaign) <= 120),
    utm_term text CHECK (utm_term IS NULL OR char_length(utm_term) <= 120),
    utm_content text CHECK (utm_content IS NULL OR char_length(utm_content) <= 120),
    enabled boolean NOT NULL DEFAULT true,
    expires_at timestamptz,
    click_limit integer CHECK (click_limit IS NULL OR click_limit > 0),
    clicks bigint NOT NULL DEFAULT 0 CHECK (clicks >= 0),
    created_by bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX links_slug_key ON links (slug);
CREATE INDEX links_site_id_id_idx ON links (site_id, id DESC);

CREATE TABLE link_clicks_daily (
    link_id bigint NOT NULL REFERENCES links(id) ON DELETE CASCADE,
    day date NOT NULL,
    country text NOT NULL DEFAULT '' CHECK (char_length(country) <= 2),
    referrer text NOT NULL DEFAULT '' CHECK (char_length(referrer) <= 100),
    device text NOT NULL DEFAULT 'desktop' CHECK (device IN ('desktop', 'mobile', 'tablet')),
    clicks bigint NOT NULL CHECK (clicks > 0),
    PRIMARY KEY (link_id, day, country, referrer, device)
);

CREATE TABLE pixel_impressions_daily (
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    day date NOT NULL,
    campaign text NOT NULL DEFAULT '' CHECK (char_length(campaign) <= 64),
    country text NOT NULL DEFAULT '' CHECK (char_length(country) <= 2),
    impressions bigint NOT NULL CHECK (impressions > 0),
    PRIMARY KEY (site_id, day, campaign, country)
);
