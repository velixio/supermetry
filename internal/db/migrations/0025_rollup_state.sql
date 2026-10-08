-- Single-row record of the rollup layout (dimension codes + counter columns)
-- that closed days were built with, plus progress of the background backfill
-- that rebuilds closed days after an upgrade changes that layout.
create table rollup_state (
  singleton boolean primary key default true check (singleton),
  layout_version text,
  dimension_set smallint[] not null default '{}',
  counter_set text[] not null default '{}',
  pending_layout_version text,
  pending_dimension_set smallint[],
  pending_counter_set text[],
  backfill_mode text check (backfill_mode in ('dimensions', 'full')),
  last_processed_site_id integer,
  last_processed_day date,
  through_site_id integer,
  through_day date,
  days_done integer not null default 0 check (days_done >= 0),
  total_days integer not null default 0 check (total_days >= 0),
  started_at timestamptz,
  finished_at timestamptz,
  check (days_done <= total_days),
  check ((last_processed_site_id is null) = (last_processed_day is null)),
  check ((through_site_id is null) = (through_day is null)),
  check ((pending_layout_version is null) = (backfill_mode is null))
);
