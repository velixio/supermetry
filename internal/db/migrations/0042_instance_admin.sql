-- Instance admins may create organizations and sites without the per-user and
-- per-organization limits. The first account on an existing instance (the one
-- created at setup) becomes an admin; new installs set it during setup.
ALTER TABLE users ADD COLUMN is_admin boolean NOT NULL DEFAULT false;
UPDATE users SET is_admin = true WHERE id = (SELECT min(id) FROM users);
