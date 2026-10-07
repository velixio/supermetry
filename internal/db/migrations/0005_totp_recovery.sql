ALTER TABLE users
    ADD COLUMN totp_recovery bytea[] NOT NULL DEFAULT '{}';
