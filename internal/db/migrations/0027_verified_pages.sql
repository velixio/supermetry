-- Provenance is established by the server's versioned path sanitizer. Existing
-- events and every import remain unverified; timestamps never establish trust.
ALTER TABLE events
    ADD COLUMN path_sanitizer_version smallint NOT NULL DEFAULT 0,
    ADD COLUMN path_policy_id text NOT NULL DEFAULT '';

-- Durable event-only closed projection. A session may contribute independently
-- to multiple policies/paths; its ordinary analytics row is not relabeled.
CREATE TABLE verified_page_sessions (
    site_id integer NOT NULL,
    day date NOT NULL,
    session_id bigint NOT NULL,
    visitor_id bigint NOT NULL,
    path_id integer NOT NULL CHECK (path_id > 0),
    path_policy_id text NOT NULL CHECK (path_policy_id <> ''),
    path_sanitizer_version smallint NOT NULL CHECK (path_sanitizer_version = 1),
    pageviews bigint NOT NULL CHECK (pageviews > 0),
    PRIMARY KEY (site_id, day, path_sanitizer_version, path_policy_id, session_id, visitor_id, path_id)
) PARTITION BY RANGE (day);

-- path_id=0 is the separately deduplicated total within this version/policy/day.
-- Counts for path_id>0 must not be summed to obtain unique visitors or sessions.
CREATE TABLE verified_page_day (
    site_id integer NOT NULL,
    day date NOT NULL,
    path_sanitizer_version smallint NOT NULL CHECK (path_sanitizer_version = 1),
    path_policy_id text NOT NULL CHECK (path_policy_id <> ''),
    path_id integer NOT NULL CHECK (path_id >= 0),
    visitors bigint NOT NULL CHECK (visitors >= 0),
    visits bigint NOT NULL CHECK (visits >= 0),
    pageviews bigint NOT NULL CHECK (pageviews >= 0),
    PRIMARY KEY (site_id, day, path_sanitizer_version, path_policy_id, path_id)
) PARTITION BY RANGE (day);
