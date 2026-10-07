CREATE TABLE orgs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL,
    slug text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE TABLE users (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email text NOT NULL,
    name text NOT NULL,
    password_hash text NOT NULL,
    totp_secret_enc bytea,
    totp_enabled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    CHECK (NOT totp_enabled OR totp_secret_enc IS NOT NULL)
);
CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));

CREATE TABLE memberships (
    org_id bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('owner', 'admin', 'editor', 'viewer')),
    site_ids integer[],
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id),
    CHECK (role IN ('editor', 'viewer') OR site_ids IS NULL)
);
CREATE INDEX memberships_user_id_idx ON memberships (user_id);

-- The analytics core already owns the partitioned table named sessions.
CREATE TABLE user_sessions (
    id_hash bytea PRIMARY KEY CHECK (octet_length(id_hash) = 32),
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    device_label text NOT NULL
);
CREATE INDEX user_sessions_user_id_idx ON user_sessions (user_id);

CREATE TABLE password_resets (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    used_at timestamptz
);
CREATE INDEX password_resets_user_id_idx ON password_resets (user_id);

CREATE TABLE invitations (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    email text NOT NULL,
    role text NOT NULL CHECK (role IN ('admin', 'editor', 'viewer')),
    site_ids integer[],
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    invited_by bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    accepted_at timestamptz,
    revoked_at timestamptz,
    CHECK (role IN ('editor', 'viewer') OR site_ids IS NULL)
);
CREATE INDEX invitations_org_id_idx ON invitations (org_id);
CREATE INDEX invitations_invited_by_idx ON invitations (invited_by);

CREATE TABLE sites (
    id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    public_id text NOT NULL UNIQUE CHECK (char_length(public_id) = 12),
    name text NOT NULL,
    allowed_hosts text[] NOT NULL DEFAULT '{}',
    timezone text NOT NULL,
    retention_class smallint NOT NULL CHECK (retention_class IN (0, 7, 30, 90, 365)),
    settings jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
CREATE INDEX sites_org_id_idx ON sites (org_id);

CREATE TABLE goals (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('page', 'event')),
    match text NOT NULL CHECK (match IN ('exact', 'prefix', 'wildcard')),
    value text NOT NULL,
    prop_key text,
    prop_value text,
    name text NOT NULL
);
CREATE INDEX goals_site_id_idx ON goals (site_id);

CREATE TABLE funnels (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    name text NOT NULL,
    steps jsonb NOT NULL
);
CREATE INDEX funnels_site_id_idx ON funnels (site_id);

CREATE TABLE api_keys (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name text NOT NULL,
    prefix text NOT NULL CHECK (char_length(prefix) = 8),
    hash bytea NOT NULL UNIQUE CHECK (octet_length(hash) = 32),
    scopes text[] NOT NULL,
    site_ids integer[],
    created_by bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    expires_at timestamptz,
    revoked_at timestamptz
);
CREATE INDEX api_keys_org_id_idx ON api_keys (org_id);
CREATE INDEX api_keys_created_by_idx ON api_keys (created_by);

CREATE TABLE shared_links (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    slug_hash bytea NOT NULL UNIQUE CHECK (octet_length(slug_hash) = 32),
    password_hash text,
    created_by bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);
CREATE INDEX shared_links_site_id_idx ON shared_links (site_id);
CREATE INDEX shared_links_created_by_idx ON shared_links (created_by);

CREATE TABLE report_subscriptions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    cadence text NOT NULL CHECK (cadence IN ('weekly', 'monthly')),
    last_sent_at timestamptz,
    UNIQUE (site_id, user_id, cadence)
);
CREATE INDEX report_subscriptions_user_id_idx ON report_subscriptions (user_id);

CREATE TABLE audit_log (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    actor_user_id bigint REFERENCES users(id) ON DELETE SET NULL,
    actor_key_id bigint REFERENCES api_keys(id) ON DELETE SET NULL,
    action text NOT NULL,
    target_type text NOT NULL,
    target_id bigint NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((actor_user_id IS NOT NULL)::integer + (actor_key_id IS NOT NULL)::integer <= 1)
);
CREATE INDEX audit_log_org_id_created_at_idx ON audit_log (org_id, created_at DESC);
CREATE INDEX audit_log_actor_user_id_idx ON audit_log (actor_user_id);
CREATE INDEX audit_log_actor_key_id_idx ON audit_log (actor_key_id);

CREATE TABLE setup_state (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    completed_at timestamptz,
    setup_token_hash bytea CHECK (setup_token_hash IS NULL OR octet_length(setup_token_hash) = 32)
);
