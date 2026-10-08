ALTER TABLE sessions_closed
    ADD COLUMN started_at timestamptz,
    ADD COLUMN profile_id bigint REFERENCES profiles(id) ON DELETE SET NULL;

UPDATE sessions_closed closed
SET started_at = hot.start
FROM sessions hot
WHERE hot.site_id = closed.site_id
  AND hot.local_day = closed.day
  AND hot.session_id = closed.session_id;

UPDATE sessions_closed
SET started_at = day::timestamptz
WHERE started_at IS NULL;

CREATE INDEX sessions_closed_site_started_idx
    ON sessions_closed (site_id, started_at DESC, session_id DESC);

CREATE INDEX sessions_closed_profile_idx
    ON sessions_closed (profile_id)
    WHERE profile_id IS NOT NULL;

-- Session timelines are looked up by (site, session ID) without knowing the
-- day; without these the lookup scans every session of the site.
CREATE INDEX sessions_site_session_idx
    ON sessions (site_id, session_id);

CREATE INDEX sessions_closed_site_session_idx
    ON sessions_closed (site_id, session_id);
