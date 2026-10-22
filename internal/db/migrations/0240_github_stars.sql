-- Star history of the instance's own GitHub repository, for the instance admin.
-- Only the numeric user id, login and star timestamps are kept: no email or
-- other profile data. Events come from the star webhook (delivery_id makes a
-- redelivery a no-op) and from the daily reconcile and one-time backfill.
CREATE TABLE github_star_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    delivery_id text UNIQUE,
    repo        text NOT NULL,
    user_id     bigint NOT NULL,
    login       text NOT NULL,
    action      text NOT NULL CHECK (action IN ('starred', 'unstarred')),
    occurred_at timestamptz NOT NULL,
    source      text NOT NULL CHECK (source IN ('webhook', 'backfill', 'reconcile'))
);
CREATE INDEX github_star_events_repo_time ON github_star_events (repo, occurred_at);
CREATE INDEX github_star_events_repo_user ON github_star_events (repo, user_id, occurred_at);

-- The live counters, one row per repository.
CREATE TABLE github_star_state (
    repo        text PRIMARY KEY,
    stars       integer NOT NULL,
    forks       integer NOT NULL DEFAULT 0,
    watchers    integer NOT NULL DEFAULT 0,
    open_issues integer NOT NULL DEFAULT 0,
    updated_at  timestamptz NOT NULL
);

-- One row per day from the reconcile job. day is the calendar day in the
-- configured time zone. Totals survive erasure of a user's events.
CREATE TABLE github_star_snapshots (
    repo        text NOT NULL,
    day         date NOT NULL,
    stars       integer NOT NULL,
    forks       integer NOT NULL DEFAULT 0,
    watchers    integer NOT NULL DEFAULT 0,
    open_issues integer NOT NULL DEFAULT 0,
    PRIMARY KEY (repo, day)
);

-- Manual markers such as a launch, shown on the chart.
CREATE TABLE github_star_annotations (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repo       text NOT NULL,
    at         timestamptz NOT NULL,
    label      text NOT NULL CHECK (char_length(label) BETWEEN 1 AND 200),
    created_by bigint REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX github_star_annotations_repo_at ON github_star_annotations (repo, at);
