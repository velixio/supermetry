ALTER TABLE events
    ADD COLUMN revenue_micros bigint NOT NULL DEFAULT 0,
    ADD COLUMN revenue_currency character(3);

ALTER TABLE custom_closed
    ADD COLUMN revenue_micros bigint NOT NULL DEFAULT 0,
    ADD COLUMN revenue_currency character(3);

ALTER TABLE goals
    ADD COLUMN currency character(3);
