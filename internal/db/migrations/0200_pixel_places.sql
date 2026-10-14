-- A counted pixel view keeps only where it came from, never who: country and city from
-- the geo lookup (the address is discarded) and whether the image proxy was GitHub's.
ALTER TABLE pixel_impressions_daily
    ADD COLUMN city text NOT NULL DEFAULT '' CHECK (char_length(city) <= 100),
    ADD COLUMN source text NOT NULL DEFAULT 'other' CHECK (source IN ('github', 'other'));

ALTER TABLE pixel_impressions_daily DROP CONSTRAINT pixel_impressions_daily_pkey;
ALTER TABLE pixel_impressions_daily
    ADD PRIMARY KEY (site_id, day, campaign, country, city, source);
