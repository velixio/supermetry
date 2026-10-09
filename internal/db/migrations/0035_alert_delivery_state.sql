ALTER TABLE alerts
    ADD COLUMN delivery_state jsonb
    CHECK (delivery_state IS NULL OR jsonb_typeof(delivery_state) = 'object');
