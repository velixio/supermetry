CREATE OR REPLACE FUNCTION lock_site_revenue_currency() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.revenue_currency_state = 'configured' AND
       (NEW.revenue_currency IS DISTINCT FROM OLD.revenue_currency OR
        NEW.revenue_currency_state IS DISTINCT FROM OLD.revenue_currency_state) THEN
        RAISE EXCEPTION 'project revenue currency is immutable' USING ERRCODE = '23514';
    ELSIF OLD.revenue_currency_state = 'conflict' AND
          (NEW.revenue_currency_state IS DISTINCT FROM 'configured' OR
           NEW.revenue_currency IS NULL OR
           NEW.revenue_currency !~ '^[A-Z]{3}$') THEN
        RAISE EXCEPTION 'project revenue currency conflict requires an explicit currency' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
