-- "Resolve in the next release" stores the newest release seen and sets this
-- flag: events from a later release then keep the issue resolved.
ALTER TABLE app_issues ADD COLUMN resolved_next boolean NOT NULL DEFAULT false;
