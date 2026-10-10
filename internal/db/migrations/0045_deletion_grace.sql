ALTER TABLE sites ADD COLUMN deleted_with_org boolean NOT NULL DEFAULT false;
ALTER TABLE sites ADD COLUMN deletion_notified_at timestamptz;
ALTER TABLE sites ADD COLUMN deletion_reminded_at timestamptz;
ALTER TABLE sites ADD COLUMN erased_at timestamptz;
UPDATE sites SET erased_at = (settings->>'erased_at')::timestamptz WHERE settings ? 'erased_at';
ALTER TABLE orgs ADD COLUMN deletion_notified_at timestamptz;
ALTER TABLE orgs ADD COLUMN deletion_reminded_at timestamptz;
ALTER TABLE orgs ADD COLUMN erased_at timestamptz;
