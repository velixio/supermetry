-- Durable, redacted application view of updater-helper jobs. The root-owned
-- helper keeps the authoritative crash-recovery record outside the database.
create table instance_update_jobs (
    id bigint generated always as identity primary key,
    requested_by bigint references users(id) on delete set null,
    idempotency_key text not null unique
        check (char_length(idempotency_key) between 8 and 128),
    from_version text not null check (char_length(from_version) between 1 and 64),
    target_version text not null check (char_length(target_version) between 1 and 64),
    deployment_mode text not null check (deployment_mode in ('binary','docker')),
    manifest_sha256 text check (manifest_sha256 is null or manifest_sha256 ~ '^[0-9a-f]{64}$'),
    status text not null check (status in (
        'requested','preflight','downloading','verifying','stopping','backing_up',
        'activating','restarting','verifying_health','complete','failed',
        'recovery_required','cancelled'
    )),
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    started_at timestamptz,
    finished_at timestamptz,
    backup_sha256 text check (backup_sha256 is null or backup_sha256 ~ '^[0-9a-f]{64}$'),
    backup_size bigint check (backup_size is null or backup_size >= 0),
    schema_before integer check (schema_before is null or schema_before >= 0),
    schema_after integer check (schema_after is null or schema_after >= 0),
    error_code text check (error_code is null or char_length(error_code) between 1 and 80),
    error_message text check (error_message is null or char_length(error_message) between 1 and 1000),
    check (target_version <> from_version),
    check (finished_at is null or finished_at >= created_at)
);

create unique index instance_update_jobs_one_active_idx
    on instance_update_jobs ((true))
    where status in (
        'requested','preflight','downloading','verifying','stopping','backing_up',
        'activating','restarting','verifying_health'
    );
create index instance_update_jobs_requested_by_idx on instance_update_jobs(requested_by);
create index instance_update_jobs_created_at_idx on instance_update_jobs(created_at desc,id desc);
