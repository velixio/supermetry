ALTER TABLE users
    ADD COLUMN totp_pending_secret_enc bytea,
    ADD COLUMN totp_pending_recovery bytea[] NOT NULL DEFAULT '{}',
    ADD COLUMN totp_last_step bigint;

ALTER TABLE users
    ADD CONSTRAINT users_totp_pending_enrollment_check
    CHECK (
        (totp_pending_secret_enc IS NULL AND cardinality(totp_pending_recovery) = 0)
        OR
        (totp_pending_secret_enc IS NOT NULL AND cardinality(totp_pending_recovery) > 0)
    );
