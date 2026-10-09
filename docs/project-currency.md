# Project revenue currency

Each project has one permanent three-letter revenue currency. New projects
select it during setup. Revenue events may omit `currency`; the server fills in
the project currency. A supplied currency must match exactly.

Revenue totals and averages always include an internal project-currency filter.
Callers may keep sending the same explicit filter for compatibility. A different
filter is rejected instead of combining or converting monetary values.

## Existing projects

Migration 0030 classifies existing projects without changing event, closed-row,
or goal currency values:

- one currency in positive hot or closed revenue becomes the project currency;
- with no recorded revenue, one goal currency becomes the project currency;
- no revenue or goal evidence remains `unconfigured` until an owner or admin
  makes the permanent selection;
- multiple, missing, or malformed historical currencies become `conflict`.

A conflict pauses new revenue intake, revenue totals, and new revenue-goal
configuration. It does not block ordinary analytics or delete historical data.
Legacy goals whose currency differs from the inferred project currency retain
their stored currency and cannot produce project-currency totals. An operator
must explicitly disable or resolve those goals later.

## Rollout boundary

The application validates revenue against the in-memory project registry when
accepting an event and checks queued events again before database flush. A stale
instance that has not observed an initial selection remains unconfigured and
rejects revenue; it cannot select a second currency because selection is
serialized in PostgreSQL and protected by an immutable database trigger.

These protections apply after every process runs the new binary and migration.
An old binary does not know the project-currency policy and can still accept or
flush revenue during a rolling upgrade. Stop old writers, deploy the migration
and new binary together, and drain or discard their queued intake before
enabling revenue. The migration will classify any writes already committed, but
it cannot inspect an old process's memory queue.

There is no foreign-exchange conversion, refund ledger, currency change, or
historical conflict-resolution workflow in this release.
