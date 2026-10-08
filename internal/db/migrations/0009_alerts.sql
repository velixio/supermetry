CREATE TABLE notification_channels (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('email', 'webhook', 'slack', 'discord')),
    name text NOT NULL,
    config jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notification_channels_org_id_idx ON notification_channels (org_id);

CREATE TABLE alerts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('traffic_spike', 'traffic_drop', 'goal_reached')),
    threshold double precision NOT NULL CHECK (threshold > 0),
    goal_id bigint REFERENCES goals(id) ON DELETE CASCADE,
    channel_ids bigint[] NOT NULL CHECK (cardinality(channel_ids) > 0),
    enabled boolean NOT NULL DEFAULT true,
    last_fired_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((kind = 'goal_reached') = (goal_id IS NOT NULL))
);
CREATE INDEX alerts_site_id_idx ON alerts (site_id);
CREATE INDEX alerts_goal_id_idx ON alerts (goal_id);
