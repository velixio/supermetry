-- Columns for Sentry-shaped events. All nullable or defaulted, so the add is
-- metadata-only and propagates to every partition.
ALTER TABLE app_events
    ADD COLUMN message_hash    bytea NULL,
    ADD COLUMN culprit         text NOT NULL DEFAULT '',
    ADD COLUMN transaction     text NOT NULL DEFAULT '',
    ADD COLUMN trace_id        bytea NULL,
    ADD COLUMN span_id         bytea NULL,
    ADD COLUMN user_hash       bytea NULL,
    ADD COLUMN sdk_name        text NOT NULL DEFAULT '',
    ADD COLUMN sdk_version     text NOT NULL DEFAULT '',
    ADD COLUMN dist            text NOT NULL DEFAULT '',
    ADD COLUMN symbolication   text NOT NULL DEFAULT '' CHECK (symbolication IN ('', 'ok', 'missing', 'partial')),
    ADD COLUMN grouping_version smallint NOT NULL DEFAULT 1;
