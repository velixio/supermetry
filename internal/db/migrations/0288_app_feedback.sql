-- User feedback sent with captureFeedback or the legacy user report, issue
-- webhook settings and the outbox that delivers them after commit.

-- The reporter's email is never stored: email_hash is an HMAC under the app
-- secret, like user_hash. name stays NULL until the opt-in to keep names
-- exists. issue_id is NULL when the event the report names is unknown.
CREATE TABLE app_feedback (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id    integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    issue_id   bigint NULL REFERENCES app_issues(id) ON DELETE SET NULL,
    event_id   text NULL CHECK (event_id IS NULL OR char_length(event_id) <= 64),
    at         timestamptz NOT NULL,
    name       text NULL CHECK (name IS NULL OR char_length(name) <= 100),
    email_hash bytea NULL,
    message    text NOT NULL CHECK (char_length(message) BETWEEN 1 AND 5000),
    url        text NULL CHECK (url IS NULL OR char_length(url) <= 2048)
);
CREATE INDEX app_feedback_site_idx ON app_feedback (site_id, at DESC);
CREATE INDEX app_feedback_issue_idx ON app_feedback (issue_id, at DESC) WHERE issue_id IS NOT NULL;

-- Per project: which existing notification channels receive issue events and
-- for which event kinds.
CREATE TABLE app_issue_webhooks (
    site_id     integer PRIMARY KEY REFERENCES sites(id) ON DELETE CASCADE,
    channel_ids bigint[] NOT NULL DEFAULT '{}',
    kinds       text[] NOT NULL DEFAULT '{}',
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Written in the transaction that changes the issue, delivered afterwards with
-- retry. The payload is built at delivery time from the issue row, so the
-- outbox holds no issue content.
CREATE TABLE app_issue_webhook_outbox (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id       integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    issue_id      bigint NOT NULL REFERENCES app_issues(id) ON DELETE CASCADE,
    kind          text NOT NULL,
    actor_user_id bigint NULL REFERENCES users(id) ON DELETE SET NULL,
    channel_ids   bigint[] NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    attempts      integer NOT NULL DEFAULT 0,
    next_at       timestamptz NOT NULL DEFAULT now(),
    done_at       timestamptz NULL
);
CREATE INDEX app_issue_webhook_outbox_due_idx ON app_issue_webhook_outbox (next_at) WHERE done_at IS NULL;
CREATE INDEX app_issue_webhook_outbox_site_idx ON app_issue_webhook_outbox (site_id);
CREATE INDEX app_issue_webhook_outbox_issue_idx ON app_issue_webhook_outbox (issue_id);
CREATE INDEX app_issue_webhook_outbox_actor_idx ON app_issue_webhook_outbox (actor_user_id) WHERE actor_user_id IS NOT NULL;
