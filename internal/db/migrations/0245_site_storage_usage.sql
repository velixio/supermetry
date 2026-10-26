CREATE TABLE storage_usage_runs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    status text NOT NULL,
    measurement_version integer NOT NULL,
    work_fingerprint text NOT NULL,
    manifest_ready boolean NOT NULL DEFAULT false,
    cursor text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    completed_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    CONSTRAINT storage_usage_runs_status_check
        CHECK (status IN ('active', 'complete', 'abandoned')),
    CONSTRAINT storage_usage_runs_measurement_version_check
        CHECK (measurement_version > 0),
    CONSTRAINT storage_usage_runs_work_fingerprint_check
        CHECK (octet_length(work_fingerprint) BETWEEN 1 AND 128),
    CONSTRAINT storage_usage_runs_completion_check
        CHECK ((status = 'complete') = (completed_at IS NOT NULL))
);

CREATE UNIQUE INDEX storage_usage_runs_one_active_idx
    ON storage_usage_runs (status) WHERE status = 'active';
CREATE INDEX storage_usage_runs_completed_at_idx
    ON storage_usage_runs (completed_at DESC) WHERE status = 'complete';

-- The usage scanner processes unpartitioned tables in bounded site-id ranges.
-- This is the only measured owner that did not already have a leading
-- site_id index in the schema.
CREATE INDEX integration_oauth_states_site_id_idx
    ON integration_oauth_states (site_id);

CREATE TABLE storage_usage_run_sites (
    run_id bigint NOT NULL REFERENCES storage_usage_runs(id) ON DELETE CASCADE,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    PRIMARY KEY (run_id, site_id)
);

CREATE INDEX storage_usage_run_sites_site_id_idx
    ON storage_usage_run_sites (site_id, run_id);

CREATE TABLE storage_usage_run_work (
    run_id bigint NOT NULL REFERENCES storage_usage_runs(id) ON DELETE CASCADE,
    ordinal integer NOT NULL,
    source_key text NOT NULL,
    work jsonb NOT NULL,
    PRIMARY KEY (run_id, ordinal),
    UNIQUE (run_id, source_key),
    CONSTRAINT storage_usage_run_work_ordinal_check CHECK (ordinal >= 0),
    CONSTRAINT storage_usage_run_work_source_key_check
        CHECK (octet_length(source_key) BETWEEN 1 AND 512)
);

CREATE TABLE storage_usage_stage (
    run_id bigint NOT NULL,
    source_key text NOT NULL,
    site_id integer NOT NULL,
    category text NOT NULL,
    bytes bigint NOT NULL,
    PRIMARY KEY (run_id, source_key, site_id, category),
    CONSTRAINT storage_usage_stage_run_site_fkey
        FOREIGN KEY (run_id, site_id)
        REFERENCES storage_usage_run_sites(run_id, site_id) ON DELETE CASCADE,
    CONSTRAINT storage_usage_stage_source_key_check
        CHECK (octet_length(source_key) BETWEEN 1 AND 512),
    CONSTRAINT storage_usage_stage_category_check
        CHECK (category IN ('hot', 'retained', 'auxiliary', 'replay_recording', 'replay_css')),
    CONSTRAINT storage_usage_stage_bytes_nonnegative_check CHECK (bytes >= 0)
);

CREATE INDEX storage_usage_stage_site_id_idx
    ON storage_usage_stage (site_id, run_id);

CREATE TABLE site_storage_usage (
    site_id integer PRIMARY KEY REFERENCES sites(id) ON DELETE CASCADE,
    hot_bytes bigint NOT NULL,
    retained_bytes bigint NOT NULL,
    auxiliary_bytes bigint NOT NULL,
    replay_recording_bytes bigint NOT NULL,
    replay_css_bytes bigint NOT NULL,
    database_bytes bigint GENERATED ALWAYS AS
        (hot_bytes + retained_bytes + auxiliary_bytes) STORED,
    replay_bytes bigint GENERATED ALWAYS AS
        (replay_recording_bytes + replay_css_bytes) STORED,
    estimated_storage_bytes bigint GENERATED ALWAYS AS
        (hot_bytes + retained_bytes + auxiliary_bytes + replay_recording_bytes + replay_css_bytes) STORED,
    measured_at timestamptz NOT NULL,
    measurement_version integer NOT NULL,
    CONSTRAINT site_storage_usage_hot_bytes_nonnegative_check CHECK (hot_bytes >= 0),
    CONSTRAINT site_storage_usage_retained_bytes_nonnegative_check CHECK (retained_bytes >= 0),
    CONSTRAINT site_storage_usage_auxiliary_bytes_nonnegative_check CHECK (auxiliary_bytes >= 0),
    CONSTRAINT site_storage_usage_replay_recording_bytes_nonnegative_check CHECK (replay_recording_bytes >= 0),
    CONSTRAINT site_storage_usage_replay_css_bytes_nonnegative_check CHECK (replay_css_bytes >= 0),
    CONSTRAINT site_storage_usage_measurement_version_check CHECK (measurement_version > 0)
);
