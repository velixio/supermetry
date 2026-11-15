-- What each app alert already sent, so a polled alert fires once per issue,
-- regression, spike hour or release (the key says which).
CREATE TABLE app_alert_fired (
    alert_id  bigint      NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    dedup_key text        NOT NULL CHECK (char_length(dedup_key) BETWEEN 1 AND 200),
    fired_at  timestamptz NOT NULL,
    PRIMARY KEY (alert_id, dedup_key)
);
