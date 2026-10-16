-- The admin API key acts as this system user, so instance audit rows written
-- through the key have a valid actor_user_id. The account can never sign in:
-- it is disabled, and '!' is not a valid password hash. is_system marks it so
-- admin lists hide it and admin mutations treat it as not found; at most one
-- system user exists.
ALTER TABLE users ADD COLUMN is_system boolean NOT NULL DEFAULT false;
CREATE UNIQUE INDEX users_single_system_key ON users (is_system) WHERE is_system;

INSERT INTO users (email, name, password_hash, is_admin, disabled_at, is_system)
VALUES ('admin-key@system.invalid', 'Admin API key', '!', true, now(), true)
ON CONFLICT DO NOTHING;
