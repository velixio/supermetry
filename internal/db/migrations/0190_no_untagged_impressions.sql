DELETE FROM pixel_impressions_daily
WHERE campaign = '';

ALTER TABLE pixel_impressions_daily
    ADD CONSTRAINT pixel_impressions_daily_campaign_not_empty
    CHECK (campaign <> '');

DELETE FROM pixel_impressions_daily AS impressions
WHERE NOT EXISTS (
    SELECT 1
    FROM tracking_pixels AS pixels
    WHERE pixels.site_id = impressions.site_id
      AND pixels.tag = impressions.campaign
);
