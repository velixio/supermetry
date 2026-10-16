-- An instance admin can sign in as a user for support. Such a session records
-- the admin in impersonated_by and carries a hard expiry that activity never
-- extends. Ordinary sessions leave both columns null. Deleting the admin ends
-- the sessions they opened.
ALTER TABLE user_sessions ADD COLUMN impersonated_by bigint REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE user_sessions ADD COLUMN hard_expires_at timestamptz;
ALTER TABLE user_sessions ADD CONSTRAINT user_sessions_impersonation_hard_expiry
    CHECK (impersonated_by IS NULL OR hard_expires_at IS NOT NULL);
CREATE INDEX user_sessions_impersonated_by_idx ON user_sessions (impersonated_by) WHERE impersonated_by IS NOT NULL;
