ALTER TABLE shared_links
    ADD COLUMN config jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD CONSTRAINT shared_links_config_object CHECK (jsonb_typeof(config) = 'object');
