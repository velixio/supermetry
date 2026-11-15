ALTER TABLE site_drop_daily DROP CONSTRAINT site_drop_daily_reason_check;
ALTER TABLE site_drop_daily ADD CONSTRAINT site_drop_daily_reason_check CHECK (reason IN (
    'bot_user_agent', 'bot_headless', 'bot_spam_referrer',
    'rate_limited_ip', 'rate_limited_site', 'rate_limited_global',
    'privacy_signal', 'dimension_overflow', 'host_not_allowed', 'clock_skew',
    'too_old', 'app_invalid', 'app_duplicate', 'app_rate_limited'
));
