ALTER TABLE rollup_day_dim
    ADD COLUMN engaged_n bigint NOT NULL DEFAULT 0;

ALTER TABLE rollup_month_dim
    ADD COLUMN engaged_n bigint NOT NULL DEFAULT 0;

ALTER TABLE rollup_block_dim
    ADD COLUMN engaged_n bigint NOT NULL DEFAULT 0;
