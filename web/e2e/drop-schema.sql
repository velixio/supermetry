-- Drops the E2E schema without exhausting the lock table.
-- A single `drop schema ... cascade` locks every table, partition and index in
-- one transaction (about 15k locks), more than a default PostgreSQL allows.
-- \gexec runs each generated drop in its own transaction instead: leaf
-- partitions first, then partitioned parents and plain tables.
-- Usage: psql "$DSN" -v ON_ERROR_STOP=1 -v schema=<name> -f e2e/drop-schema.sql
set client_min_messages = warning;
select format('drop table if exists %I.%I cascade', n.nspname, c.relname)
from pg_class c
join pg_namespace n on n.oid = c.relnamespace
where n.nspname = :'schema' and c.relkind in ('r', 'p')
order by c.relispartition desc, c.relkind = 'p', c.oid desc
\gexec
drop schema if exists :"schema" cascade;
