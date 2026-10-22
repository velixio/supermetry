-- Repositories tracked for GitHub stars, configured by the instance admin.
-- repo is stored lowercase as owner/name and is the key the star event,
-- state, snapshot, annotation and erasure tables already use. The webhook
-- secret and the optional API token are encrypted with the instance secret key
-- and never returned by the API after creation.
CREATE TABLE github_star_repos (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id         text NOT NULL UNIQUE,
    repo              text NOT NULL UNIQUE CHECK (repo = lower(repo) AND repo ~ '^[a-z0-9_.-]+/[a-z0-9_.-]+$'),
    enabled           boolean NOT NULL DEFAULT true,
    timezone          text NOT NULL DEFAULT 'UTC',
    webhook_secret_enc bytea NOT NULL,
    token_enc         bytea,
    created_at        timestamptz NOT NULL DEFAULT now(),
    last_webhook_at   timestamptz,
    last_reconcile_at timestamptz,
    last_error        text
);
