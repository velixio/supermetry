-- PagerDuty channels and the weekly digest switch.
ALTER TABLE notification_channels
    DROP CONSTRAINT notification_channels_kind_check,
    ADD CONSTRAINT notification_channels_kind_check
        CHECK (kind IN ('email', 'webhook', 'slack', 'discord', 'telegram', 'pagerduty'));

ALTER TABLE notification_templates
    DROP CONSTRAINT notification_templates_kind_check,
    ADD CONSTRAINT notification_templates_kind_check
        CHECK (kind IN ('email', 'webhook', 'slack', 'discord', 'telegram', 'pagerduty'));

-- 'none' is the placeholder kind of suppressed rows (alert silences); keeping it
-- here means the two migrations can land in either order.
ALTER TABLE alert_delivery_log
    DROP CONSTRAINT alert_delivery_log_channel_kind_check,
    ADD CONSTRAINT alert_delivery_log_channel_kind_check
        CHECK (channel_kind IN ('email', 'webhook', 'slack', 'discord', 'telegram', 'pagerduty', 'none'));

-- weekly_digest is per user and project; last_digest_week is the ISO week
-- (2026-W47) the digest was last mailed for, so a retry never mails twice.
ALTER TABLE user_notification_prefs
    ADD COLUMN weekly_digest   boolean NOT NULL DEFAULT false,
    ADD COLUMN last_digest_week text NULL CHECK (last_digest_week IS NULL OR last_digest_week ~ '^[0-9]{4}-W[0-9]{2}$');

-- The digest job scans only the users who opted in.
CREATE INDEX user_notification_prefs_digest_idx ON user_notification_prefs (site_id) WHERE weekly_digest;
