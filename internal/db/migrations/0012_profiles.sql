ALTER TABLE sites
    ALTER COLUMN settings SET DEFAULT '{"profiles_enabled": false}'::jsonb;

UPDATE sites
SET settings = jsonb_set(
    settings,
    '{profiles_enabled}',
    COALESCE(settings->'profiles_enabled', 'false'::jsonb),
    true
);

CREATE TABLE profiles (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    external_id text NOT NULL CHECK (char_length(external_id) BETWEEN 1 AND 128),
    traits jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(traits) = 'object'),
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    UNIQUE (site_id, external_id),
    CHECK (first_seen_at <= last_seen_at)
);

ALTER TABLE events
    ADD COLUMN profile_id bigint;

-- Profile timelines always constrain site and non-null profile ID. Keeping
-- this partial avoids indexing anonymous events, while creation on the
-- partitioned parent gives every existing and future leaf the same index.
CREATE INDEX events_profile_timeline_idx
    ON events (site_id, profile_id, ts DESC)
    WHERE profile_id IS NOT NULL;
