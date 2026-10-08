alter table users add column deleted_at timestamptz;

create table email_changes (
    token_hash bytea primary key check (octet_length(token_hash) = 32),
    user_id bigint not null references users(id) on delete cascade,
    new_email text not null,
    expires_at timestamptz not null,
    used_at timestamptz
);
create index email_changes_user_id_idx on email_changes (user_id);
