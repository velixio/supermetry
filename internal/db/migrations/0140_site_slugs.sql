-- Readable site URLs. A slug is unique per organization among live sites and is
-- filled from the first allowed host when a site is inserted without one, so
-- every insert path gets one. Dashboard links resolve a slug first and fall back
-- to public_id, so links built from public_id keep working.
ALTER TABLE sites ADD COLUMN slug text;

-- The base slug for a site: its first host without a leading "*." or "www.",
-- lowercased, with runs of other characters turned into "-". Reserved words and
-- twelve-character values (which could be taken for a public_id) get a suffix.
CREATE FUNCTION site_slug_base(hosts text[], site_name text) RETURNS text
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    base text;
BEGIN
    base := lower(coalesce(nullif(hosts[1], ''), site_name, ''));
    base := regexp_replace(base, '^(\*\.)?(www\.)?', '');
    base := regexp_replace(base, '[^a-z0-9]+', '-', 'g');
    base := trim(both '-' from base);
    base := trim(both '-' from left(base, 48));
    IF base = '' THEN
        base := 'site';
    END IF;
    IF base IN ('sites', 'settings', 'health', 'new') OR base ~ '^[a-z0-9]{12}$' THEN
        base := base || '-site';
    END IF;
    RETURN base;
END;
$$;

-- The first free slug for base within an organization: base, base-2, base-3, ...
CREATE FUNCTION site_slug_free(in_org bigint, base text, except_id integer) RETURNS text
LANGUAGE plpgsql AS $$
DECLARE
    candidate text := base;
    n integer := 1;
BEGIN
    WHILE EXISTS (
        SELECT 1 FROM sites
        WHERE org_id = in_org AND slug = candidate AND deleted_at IS NULL
          AND (except_id IS NULL OR id <> except_id)
    ) LOOP
        n := n + 1;
        candidate := base || '-' || n;
    END LOOP;
    RETURN candidate;
END;
$$;

DO $$
DECLARE
    item record;
BEGIN
    FOR item IN SELECT id, org_id, allowed_hosts, name FROM sites ORDER BY id LOOP
        UPDATE sites
        SET slug = site_slug_free(item.org_id, site_slug_base(item.allowed_hosts, item.name), item.id)
        WHERE id = item.id;
    END LOOP;
END;
$$;

ALTER TABLE sites ALTER COLUMN slug SET NOT NULL;
ALTER TABLE sites ADD CONSTRAINT sites_slug_format CHECK (
    char_length(slug) BETWEEN 1 AND 63
    AND slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'
    AND slug NOT IN ('sites', 'settings', 'health', 'new')
    AND slug !~ '^[a-z0-9]{12}$'
);
CREATE UNIQUE INDEX sites_org_slug_key ON sites (org_id, slug) WHERE deleted_at IS NULL;

CREATE FUNCTION sites_fill_slug() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' AND NEW.slug IS NULL THEN
        PERFORM pg_advisory_xact_lock(hashtext('site_slug:' || NEW.org_id));
        NEW.slug := site_slug_free(NEW.org_id, site_slug_base(NEW.allowed_hosts, NEW.name), NULL);
    ELSIF TG_OP = 'UPDATE' AND OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS NULL THEN
        -- A restored site gives up its slug if a live site took it meanwhile.
        PERFORM pg_advisory_xact_lock(hashtext('site_slug:' || NEW.org_id));
        NEW.slug := site_slug_free(NEW.org_id, NEW.slug, NEW.id);
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER sites_fill_slug BEFORE INSERT OR UPDATE OF deleted_at ON sites
    FOR EACH ROW EXECUTE FUNCTION sites_fill_slug();
