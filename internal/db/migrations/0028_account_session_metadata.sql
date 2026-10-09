-- Account sign-in metadata only; analytics visitor sessions are unchanged.
-- NULL preserves unknown metadata for existing sessions, without backfilling.
ALTER TABLE user_sessions
    ADD COLUMN ip_address inet,
    ADD COLUMN country_code text CHECK (country_code ~ '^[A-Z]{2}$'),
    ADD COLUMN browser text CHECK (length(browser) BETWEEN 1 AND 80);
