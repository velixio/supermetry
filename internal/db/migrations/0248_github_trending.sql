-- Trending watcher. Opt-in per tracked repository; language is the repository's
-- primary language as the star tracker last saw it (empty = unknown).
ALTER TABLE github_star_repos
    ADD COLUMN trending_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN language text NOT NULL DEFAULT '';

-- One row per episode: a repository is in a trending list from first_seen_at
-- until it has been absent from two consecutive readable fetches of that list
-- (misses counts the absences so far). list_language is the lowercase language
-- of the list, empty for the all-languages list. since is GitHub's ?since=
-- window. annotation_id points at the chart marker written on entry, without
-- a foreign key: the marker is a user-visible row they may delete.
CREATE TABLE github_trending_sightings (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repo_id       bigint NOT NULL REFERENCES github_star_repos(id) ON DELETE CASCADE,
    list_language text NOT NULL DEFAULT '',
    since         text NOT NULL CHECK (since IN ('daily', 'weekly')),
    first_rank    integer NOT NULL CHECK (first_rank > 0),
    best_rank     integer NOT NULL CHECK (best_rank > 0),
    last_rank     integer NOT NULL CHECK (last_rank > 0),
    first_seen_at timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL,
    left_at       timestamptz,
    misses        integer NOT NULL DEFAULT 0,
    annotation_id bigint
);
CREATE UNIQUE INDEX github_trending_sightings_open
    ON github_trending_sightings (repo_id, list_language, since) WHERE left_at IS NULL;
CREATE INDEX github_trending_sightings_history
    ON github_trending_sightings (repo_id, first_seen_at DESC);

-- Health of each list the job fetches. parse_ok is false while the page cannot
-- be read; skip_until pauses a language GitHub has no list for.
CREATE TABLE github_trending_list_state (
    list_language   text NOT NULL DEFAULT '',
    since           text NOT NULL CHECK (since IN ('daily', 'weekly')),
    last_ok_at      timestamptz,
    last_attempt_at timestamptz,
    last_error      text NOT NULL DEFAULT '',
    parse_ok        boolean NOT NULL DEFAULT true,
    entries         integer NOT NULL DEFAULT 0,
    skip_until      timestamptz,
    PRIMARY KEY (list_language, since)
);

-- The last run, for status: how many lists were wanted, fetched and postponed
-- by the per-run request cap.
CREATE TABLE github_trending_run (
    id             smallint PRIMARY KEY CHECK (id = 1),
    last_run_at    timestamptz NOT NULL,
    lists_needed   integer NOT NULL,
    lists_fetched  integer NOT NULL,
    lists_deferred integer NOT NULL
);
