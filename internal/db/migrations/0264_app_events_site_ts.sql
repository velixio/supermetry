-- Overview, compatibility and release reads filter by site and time range;
-- with several apps on one instance the BRIN on ts alone cannot narrow by site.
CREATE INDEX app_events_site_ts_idx ON app_events (site_id, ts);
