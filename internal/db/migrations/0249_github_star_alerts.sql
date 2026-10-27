-- Star alerts for tracked GitHub repositories. Alerts are delivered through the
-- organization's existing notification channels (notification_channels).

-- Which events are on for a repository. Absent means off; every event is off
-- until an owner or admin ticks it.
CREATE TABLE github_star_alert_settings (
    repo_id bigint PRIMARY KEY REFERENCES github_star_repos(id) ON DELETE CASCADE,
    events jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(events) = 'object'),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Which channels receive them. Deleting a channel removes it from every
-- repository's settings; the service only accepts channels of the repository's
-- own organization.
CREATE TABLE github_star_alert_channels (
    repo_id bigint NOT NULL REFERENCES github_star_repos(id) ON DELETE CASCADE,
    channel_id bigint NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    PRIMARY KEY (repo_id, channel_id)
);
CREATE INDEX github_star_alert_channels_channel_idx ON github_star_alert_channels (channel_id);

-- Last star count the evaluator saw. A missing row means "not evaluated yet":
-- the first evaluation only records a baseline, so enabling a repository that
-- already has thousands of stars sends nothing.
CREATE TABLE github_star_alert_state (
    repo_id bigint PRIMARY KEY REFERENCES github_star_repos(id) ON DELETE CASCADE,
    stars integer NOT NULL CHECK (stars >= 0),
    evaluated_at timestamptz NOT NULL
);

-- Sent alerts: the unique key makes each event go out once, and the recent
-- rows enforce the per-repository, per-kind minimum interval.
CREATE TABLE github_star_alert_log (
    repo_id bigint NOT NULL REFERENCES github_star_repos(id) ON DELETE CASCADE,
    kind text NOT NULL,
    key text NOT NULL,
    sent_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repo_id, kind, key)
);
CREATE INDEX github_star_alert_log_recent_idx ON github_star_alert_log (repo_id, kind, sent_at DESC);
