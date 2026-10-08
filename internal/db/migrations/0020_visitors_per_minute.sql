CREATE TABLE visitors_per_minute (
    site_id integer NOT NULL,
    minute timestamptz NOT NULL,
    visitors integer NOT NULL,
    pageviews integer NOT NULL,
    events integer NOT NULL,
    PRIMARY KEY (site_id, minute)
);
