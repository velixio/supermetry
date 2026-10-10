ALTER TABLE sites ADD COLUMN erasure_started_at timestamptz;

CREATE TABLE deletion_notice_sends (
    target_type text NOT NULL CHECK (target_type IN ('site', 'org')),
    target_id bigint NOT NULL,
    kind text NOT NULL CHECK (kind IN ('scheduled', 'reminder')),
    deleted_at timestamptz NOT NULL,
    user_id bigint NOT NULL,
    sent_at timestamptz NOT NULL,
    PRIMARY KEY (target_type, target_id, kind, deleted_at, user_id)
);
