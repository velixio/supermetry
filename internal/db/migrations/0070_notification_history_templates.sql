CREATE TABLE notification_templates (
    org_id bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('email', 'webhook', 'slack', 'discord', 'telegram')),
    subject_template text NOT NULL DEFAULT '',
    body_template text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, kind),
    CHECK (octet_length(subject_template) <= 200),
    CHECK (octet_length(body_template) BETWEEN 1 AND 4096),
    CHECK (kind = 'email' OR subject_template = '')
);

CREATE TABLE notification_settings (
    org_id bigint PRIMARY KEY REFERENCES orgs(id) ON DELETE CASCADE,
    history_retention_days smallint NOT NULL DEFAULT 30
        CHECK (history_retention_days BETWEEN 1 AND 3650),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE alert_delivery_log (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    alert_id bigint NOT NULL,
    channel_id bigint NOT NULL,
    channel_kind text NOT NULL CHECK (channel_kind IN ('email', 'webhook', 'slack', 'discord', 'telegram')),
    channel_name text NOT NULL,
    status text NOT NULL CHECK (status IN ('pending', 'succeeded', 'failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    error_text text NOT NULL DEFAULT '' CHECK (octet_length(error_text) <= 512),
    notification jsonb NOT NULL CHECK (jsonb_typeof(notification) = 'object'),
    fired_at timestamptz NOT NULL,
    last_attempt_at timestamptz,
    delivered_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (alert_id, channel_id, fired_at)
);

CREATE INDEX alert_delivery_log_site_created_idx
    ON alert_delivery_log (site_id, created_at DESC, id DESC);
CREATE INDEX alert_delivery_log_org_created_idx
    ON alert_delivery_log (org_id, created_at, id);
