CREATE TABLE open_counters (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    key text NOT NULL UNIQUE CHECK (key = lower(key) AND char_length(key) <= 160),
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_day date NOT NULL
);

CREATE INDEX open_counters_last_seen_day_idx ON open_counters(last_seen_day);

CREATE TABLE open_counter_views_daily (
    counter_id bigint NOT NULL REFERENCES open_counters(id) ON DELETE CASCADE,
    day date NOT NULL,
    views bigint NOT NULL CHECK (views > 0),
    PRIMARY KEY(counter_id, day)
);

CREATE INDEX open_counter_views_daily_day_idx ON open_counter_views_daily USING brin(day);
