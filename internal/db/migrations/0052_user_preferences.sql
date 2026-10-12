ALTER TABLE users
ADD COLUMN preferences jsonb NOT NULL DEFAULT '{}'::jsonb,
ADD CONSTRAINT users_preferences_object CHECK (jsonb_typeof(preferences) = 'object');
