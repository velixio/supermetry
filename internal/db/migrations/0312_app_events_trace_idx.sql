-- Errors linked to a trace: the trace view looks up app_events by trace id.
-- Partial, so events without a trace (most of them) cost nothing.
CREATE INDEX app_events_trace_idx ON app_events (site_id, trace_id) WHERE trace_id IS NOT NULL;
