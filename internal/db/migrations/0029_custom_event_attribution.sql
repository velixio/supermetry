-- Preserve the event's own traffic attribution when raw custom events are
-- compacted. NULL means the row predates this evidence; zero is an observed
-- absence and must never fall back to the enclosing session's acquisition.
ALTER TABLE custom_closed
    ADD COLUMN event_source_id integer,
    ADD COLUMN event_utm_source_id integer,
    ADD COLUMN event_utm_medium_id integer,
    ADD COLUMN event_utm_campaign_id integer,
    ADD COLUMN event_utm_content_id integer,
    ADD COLUMN event_utm_term_id integer;
