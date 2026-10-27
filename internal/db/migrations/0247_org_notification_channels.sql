-- Per-organization notification channels (Telegram first) that features use to
-- tell an org's owners about events such as GitHub star milestones.
CREATE TABLE org_notification_channels (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    kind text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    -- Sealed bot token (secret box); never returned by the API.
    config_enc bytea NOT NULL,
    chat_id text NOT NULL,
    -- Map of event kind to boolean; an absent kind means off.
    events jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    last_ok_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    consecutive_failures integer NOT NULL DEFAULT 0,
    CONSTRAINT org_notification_channels_kind_check CHECK (kind IN ('telegram')),
    CONSTRAINT org_notification_channels_events_check CHECK (jsonb_typeof(events) = 'object'),
    CONSTRAINT org_notification_channels_failures_check CHECK (consecutive_failures >= 0),
    CONSTRAINT org_notification_channels_org_kind_key UNIQUE (org_id, kind)
);

-- Sent-event log used for dedup (unique key) and the per-kind minimum interval.
CREATE TABLE org_notification_log (
    org_id bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    kind text NOT NULL,
    key text NOT NULL,
    sent_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, kind, key)
);
CREATE INDEX org_notification_log_recent_idx ON org_notification_log (org_id, kind, sent_at DESC);
