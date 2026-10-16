-- Hourly impression counts for the tracking-pixel chart on short ranges. Only a
-- count per site, campaign and hour is kept: no place, source or visitor data.
-- hour is the wall-clock hour in the site's time zone, like the daily table's day.
-- Rows older than 30 days are deleted by the retention job.
CREATE TABLE pixel_impressions_hourly (
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    campaign text NOT NULL CHECK (char_length(campaign) BETWEEN 1 AND 64),
    hour timestamp NOT NULL CHECK (date_trunc('hour', hour) = hour),
    impressions bigint NOT NULL CHECK (impressions > 0),
    PRIMARY KEY (site_id, campaign, hour)
);
