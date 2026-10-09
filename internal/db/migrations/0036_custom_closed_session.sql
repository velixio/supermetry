ALTER TABLE custom_closed
    ADD COLUMN session_id bigint;

ALTER TABLE sessions_closed
    ADD COLUMN seq_at timestamptz[];
