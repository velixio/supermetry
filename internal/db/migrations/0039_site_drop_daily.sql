CREATE TABLE site_drop_daily (
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    day date NOT NULL,
    reason text NOT NULL CHECK (reason IN (
        'bot_user_agent', 'bot_headless', 'bot_spam_referrer',
        'rate_limited_ip', 'rate_limited_site', 'rate_limited_global',
        'privacy_signal', 'dimension_overflow', 'host_not_allowed', 'clock_skew'
    )),
    count bigint NOT NULL CHECK (count >= 0),
    PRIMARY KEY (site_id, day, reason)
);
CREATE INDEX site_drop_daily_day_idx ON site_drop_daily(day);

CREATE TABLE site_bot_rule_daily (
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    day date NOT NULL,
    rule text NOT NULL CHECK (char_length(rule) BETWEEN 1 AND 160),
    count bigint NOT NULL CHECK (count >= 0),
    PRIMARY KEY (site_id, day, rule)
);
CREATE INDEX site_bot_rule_daily_day_idx ON site_bot_rule_daily(day);
