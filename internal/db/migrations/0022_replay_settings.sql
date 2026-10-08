create table instance_settings (
    id boolean primary key default true check (id),
    settings jsonb not null default '{}'::jsonb,
    updated_at timestamptz not null default now()
);

insert into instance_settings default values;
