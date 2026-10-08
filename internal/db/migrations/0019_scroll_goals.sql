ALTER TABLE goals DROP CONSTRAINT goals_kind_check;
ALTER TABLE goals ADD CONSTRAINT goals_kind_check
    CHECK (kind IN ('page', 'event', 'scroll'));
ALTER TABLE goals ADD COLUMN scroll_threshold smallint
    CHECK (scroll_threshold BETWEEN 1 AND 100);
ALTER TABLE goals ADD CONSTRAINT goals_scroll_threshold_kind_check
    CHECK ((kind = 'scroll') = (scroll_threshold IS NOT NULL));
