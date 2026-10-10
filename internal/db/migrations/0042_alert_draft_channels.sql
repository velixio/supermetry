ALTER TABLE alerts DROP CONSTRAINT alerts_channel_ids_check;
ALTER TABLE alerts ADD CONSTRAINT alerts_channel_ids_check
    CHECK (NOT enabled OR cardinality(channel_ids) > 0);
