-- Scheduled site exports to S3-compatible storage. The secret is stored
-- encrypted (platform crypto.SecretBox); it is never returned by the API.
CREATE TABLE export_schedules (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    site_id integer REFERENCES sites(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    frequency text NOT NULL CHECK (frequency IN ('daily', 'weekly', 'monthly')),
    incremental boolean NOT NULL DEFAULT true,
    enabled boolean NOT NULL DEFAULT true,
    endpoint text NOT NULL,
    region text NOT NULL,
    bucket text NOT NULL,
    prefix text NOT NULL DEFAULT '',
    access_key_id text NOT NULL,
    secret_enc bytea NOT NULL,
    path_style boolean NOT NULL DEFAULT false,
    next_run_at timestamptz NOT NULL,
    last_run_at timestamptz,
    last_status text CHECK (last_status IN ('succeeded', 'failed', 'skipped')),
    last_error text NOT NULL DEFAULT '',
    created_by bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX export_schedules_org_id_idx ON export_schedules (org_id);
CREATE INDEX export_schedules_site_id_idx ON export_schedules (site_id) WHERE site_id IS NOT NULL;
CREATE INDEX export_schedules_due_idx ON export_schedules (next_run_at) WHERE enabled;

-- Last day exported per schedule and site, for incremental runs.
CREATE TABLE export_schedule_progress (
    schedule_id bigint NOT NULL REFERENCES export_schedules(id) ON DELETE CASCADE,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    exported_through date NOT NULL,
    PRIMARY KEY (schedule_id, site_id)
);

-- One row per site export attempt.
CREATE TABLE export_runs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    schedule_id bigint NOT NULL REFERENCES export_schedules(id) ON DELETE CASCADE,
    site_id integer REFERENCES sites(id) ON DELETE SET NULL,
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    status text NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'skipped')),
    from_day date,
    to_day date,
    bytes bigint NOT NULL DEFAULT 0,
    object_key text NOT NULL DEFAULT '',
    error text NOT NULL DEFAULT ''
);
CREATE INDEX export_runs_schedule_idx ON export_runs (schedule_id, id DESC);
