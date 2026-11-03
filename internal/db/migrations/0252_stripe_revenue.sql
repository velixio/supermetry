-- Stripe revenue: one webhook endpoint per site, the processed Stripe event
-- ids (idempotency), the acquisition channel remembered per Stripe customer for
-- renewals, and a refund ledger. Disconnecting deletes every row of the site.

CREATE TABLE stripe_connections (
    site_id integer PRIMARY KEY REFERENCES sites(id) ON DELETE CASCADE,
    org_id bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    -- Opaque path segment of the webhook URL (32 random bytes, base64url).
    endpoint_id text NOT NULL UNIQUE CHECK (endpoint_id ~ '^[A-Za-z0-9_-]{43}$'),
    -- Sealed whsec_ signing secret; NULL until the user pastes it.
    signing_secret bytea CHECK (signing_secret IS NULL OR octet_length(signing_secret) BETWEEN 1 AND 1024),
    -- Random per-connection key for the keyed hashes of Stripe object ids.
    hash_key bytea NOT NULL CHECK (octet_length(hash_key) = 32),
    record_payment_intents boolean NOT NULL DEFAULT false,
    connected_by bigint REFERENCES users(id) ON DELETE SET NULL,
    events_received bigint NOT NULL DEFAULT 0 CHECK (events_received >= 0),
    revenue_events bigint NOT NULL DEFAULT 0 CHECK (revenue_events >= 0),
    last_event_at timestamptz,
    last_event_type text NOT NULL DEFAULT '' CHECK (octet_length(last_event_type) <= 64),
    last_event_livemode boolean,
    last_error text NOT NULL DEFAULT '' CHECK (octet_length(last_error) <= 256),
    last_error_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Stripe delivers at least once; a row per handled event id makes retries no-ops.
CREATE TABLE stripe_webhook_events (
    site_id integer NOT NULL REFERENCES stripe_connections(site_id) ON DELETE CASCADE,
    event_id text NOT NULL CHECK (octet_length(event_id) BETWEEN 1 AND 255),
    received_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (site_id, event_id)
);

CREATE INDEX stripe_webhook_events_received_idx ON stripe_webhook_events (received_at);

-- The channel that acquired a customer, by keyed hash of the Stripe customer
-- id. Values are attribution strings, not dictionary ids, so dictionary
-- garbage collection cannot invalidate them. No personal data is stored.
CREATE TABLE stripe_customers (
    site_id integer NOT NULL REFERENCES stripe_connections(site_id) ON DELETE CASCADE,
    customer_key bytea NOT NULL CHECK (octet_length(customer_key) = 32),
    source text NOT NULL DEFAULT '' CHECK (octet_length(source) <= 512),
    utm_source text NOT NULL DEFAULT '' CHECK (octet_length(utm_source) <= 512),
    medium text NOT NULL DEFAULT '' CHECK (octet_length(medium) <= 512),
    campaign text NOT NULL DEFAULT '' CHECK (octet_length(campaign) <= 512),
    country text NOT NULL DEFAULT '' CHECK (octet_length(country) <= 2),
    product text NOT NULL DEFAULT '' CHECK (octet_length(product) <= 200),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (site_id, customer_key)
);

CREATE INDEX stripe_customers_last_seen_idx ON stripe_customers (last_seen_at);

-- Refunds are kept apart from revenue events: revenue amounts are never
-- negative. amount_micros is the cumulative refunded amount of the charge.
CREATE TABLE stripe_refunds (
    site_id integer NOT NULL REFERENCES stripe_connections(site_id) ON DELETE CASCADE,
    charge_key bytea NOT NULL CHECK (octet_length(charge_key) = 32),
    amount_micros bigint NOT NULL CHECK (amount_micros >= 0),
    currency char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    refunded_at timestamptz NOT NULL,
    PRIMARY KEY (site_id, charge_key)
);

CREATE INDEX stripe_refunds_refunded_at_idx ON stripe_refunds (refunded_at);
