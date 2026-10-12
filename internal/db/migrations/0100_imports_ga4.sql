ALTER TABLE imports DROP CONSTRAINT IF EXISTS imports_source_check;
ALTER TABLE imports ADD CONSTRAINT imports_source_check
    CHECK (source IN ('plausible', 'umami', 'ga4'));
