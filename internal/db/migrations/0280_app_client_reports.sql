-- Daily counts of events the SDKs report they discarded (Sentry client
-- reports), per reason and data category. Kept as long as the app's retention
-- class; removed with the site.
CREATE TABLE app_client_reports (
    site_id  integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    day      date NOT NULL,
    reason   text NOT NULL,
    category text NOT NULL,
    quantity bigint NOT NULL CHECK (quantity >= 0),
    PRIMARY KEY (site_id, day, reason, category)
);
