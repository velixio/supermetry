-- Additive grants never imply billing authority. Billing Admin is independent
-- of the four base roles. Empty site_ids remains an explicit no-sites grant.
ALTER TABLE memberships
    ADD COLUMN capabilities text[] NOT NULL DEFAULT '{}',
    ADD COLUMN billing_admin boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT memberships_capabilities_valid CHECK (
        capabilities <@ ARRAY['profiles.read','replay.read','data.export']::text[]
        AND array_position(capabilities, NULL) IS NULL
    );

ALTER TABLE invitations
    ADD COLUMN capabilities text[] NOT NULL DEFAULT '{}',
    ADD COLUMN billing_admin boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT invitations_capabilities_valid CHECK (
        capabilities <@ ARRAY['profiles.read','replay.read','data.export']::text[]
        AND array_position(capabilities, NULL) IS NULL
    );
