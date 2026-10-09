ALTER TABLE notification_channels
    DROP CONSTRAINT notification_channels_kind_check,
    ADD CONSTRAINT notification_channels_kind_check
        CHECK (kind IN ('email', 'webhook', 'slack', 'discord', 'telegram')),
    ADD COLUMN enabled boolean NOT NULL DEFAULT true;
