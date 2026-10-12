-- Reserve application route words before installing the invariant so an
-- existing organization is never made unreachable during upgrade.
do $$
declare
    organization record;
    candidate text;
begin
    for organization in
        select id,lower(slug) as slug from orgs
        where lower(slug) = any(array[
            'account','admin','api','auth','invite','login','logout',
            'password-reset','reset','setup','share','signup'
        ])
        order by id
    loop
        candidate := organization.slug || '-org-' || organization.id::text;
        while exists(select 1 from orgs where slug=candidate and id<>organization.id) loop
            candidate := candidate || '-x';
        end loop;
        update orgs set slug=candidate where id=organization.id;
    end loop;
end $$;

alter table orgs add constraint orgs_slug_not_reserved check (
    lower(slug) <> all(array[
        'account','admin','api','auth','invite','login','logout',
        'password-reset','reset','setup','share','signup'
    ])
);

alter table users
    add column disabled_at timestamptz,
    add column password_reset_required boolean not null default false,
    add column two_factor_grace_started_at timestamptz;

alter table instance_settings
    add column signup_mode_override text check (signup_mode_override in ('invite','open')),
    add column replay_visible_override boolean,
    add column two_factor_scope text not null default 'off'
        check (two_factor_scope in ('off','instance_admins','owners_admins','everyone')),
    add column two_factor_grace_days smallint not null default 7
        check (two_factor_grace_days between 0 and 30),
    add column policy_changed_at timestamptz not null default now();

create table two_factor_exemptions (
    user_id bigint primary key references users(id) on delete cascade,
    granted_by bigint not null references users(id) on delete restrict,
    reason text not null check (char_length(reason) between 1 and 500),
    expires_at timestamptz,
    created_at timestamptz not null default now(),
    check (expires_at is null or expires_at > created_at)
);
create index two_factor_exemptions_granted_by_idx on two_factor_exemptions(granted_by);

alter table audit_log alter column org_id drop not null;
create index audit_log_instance_created_at_idx on audit_log(created_at desc,id desc) where org_id is null;
