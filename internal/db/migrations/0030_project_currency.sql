ALTER TABLE sites
    ADD COLUMN revenue_currency character(3),
    ADD COLUMN revenue_currency_state text NOT NULL DEFAULT 'unconfigured';

WITH recorded AS (
    SELECT site_id, array_agg(DISTINCT currency ORDER BY currency) currencies
    FROM (
        SELECT site_id, coalesce(revenue_currency::text, '') currency
        FROM events
        WHERE revenue_micros > 0
        UNION
        SELECT site_id, coalesce(revenue_currency::text, '') currency
        FROM custom_closed
        WHERE revenue_micros > 0
    ) values_by_site
    GROUP BY site_id
)
UPDATE sites s
SET revenue_currency = CASE WHEN cardinality(r.currencies) = 1 AND r.currencies[1] ~ '^[A-Z]{3}$' THEN r.currencies[1] END,
    revenue_currency_state = CASE WHEN cardinality(r.currencies) = 1 AND r.currencies[1] ~ '^[A-Z]{3}$' THEN 'configured' ELSE 'conflict' END
FROM recorded r
WHERE r.site_id = s.id;

WITH goal_currencies AS (
    SELECT site_id, array_agg(DISTINCT currency::text ORDER BY currency::text) currencies
    FROM goals
    WHERE currency IS NOT NULL
    GROUP BY site_id
)
UPDATE sites s
SET revenue_currency = CASE WHEN cardinality(g.currencies) = 1 AND g.currencies[1] ~ '^[A-Z]{3}$' THEN g.currencies[1] END,
    revenue_currency_state = CASE WHEN cardinality(g.currencies) = 1 AND g.currencies[1] ~ '^[A-Z]{3}$' THEN 'configured' ELSE 'conflict' END
FROM goal_currencies g
WHERE g.site_id = s.id AND s.revenue_currency_state = 'unconfigured';

ALTER TABLE sites
    ADD CONSTRAINT sites_revenue_currency_state_check CHECK (
        (revenue_currency_state = 'configured' AND revenue_currency ~ '^[A-Z]{3}$') OR
        (revenue_currency_state IN ('unconfigured', 'conflict') AND revenue_currency IS NULL)
    );

CREATE FUNCTION lock_site_revenue_currency() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.revenue_currency_state <> 'unconfigured' AND
       (NEW.revenue_currency IS DISTINCT FROM OLD.revenue_currency OR
        NEW.revenue_currency_state IS DISTINCT FROM OLD.revenue_currency_state) THEN
        RAISE EXCEPTION 'project revenue currency is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER sites_lock_revenue_currency
BEFORE UPDATE OF revenue_currency, revenue_currency_state ON sites
FOR EACH ROW EXECUTE FUNCTION lock_site_revenue_currency();
