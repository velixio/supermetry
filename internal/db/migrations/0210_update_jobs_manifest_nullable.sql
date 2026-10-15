-- The manifest digest is only known after the helper downloads the release, so a job is
-- created without it. Some databases created this column NOT NULL; relax it everywhere.
alter table instance_update_jobs alter column manifest_sha256 drop not null;
