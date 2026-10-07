CREATE TABLE schema_migrations(
    version int PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);
