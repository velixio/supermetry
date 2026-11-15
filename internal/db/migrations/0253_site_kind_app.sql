ALTER TABLE sites DROP CONSTRAINT sites_kind_check;
ALTER TABLE sites ADD CONSTRAINT sites_kind_check
    CHECK (kind IN ('web', 'pixel', 'app'));
