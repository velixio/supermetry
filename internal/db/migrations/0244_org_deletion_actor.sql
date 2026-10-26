ALTER TABLE orgs
    ADD COLUMN deleted_by_kind text,
    ADD COLUMN deleted_by_user_id bigint REFERENCES users(id) ON DELETE SET NULL,
    ADD CONSTRAINT orgs_deleted_by_kind_check
        CHECK (deleted_by_kind IS NULL OR deleted_by_kind IN ('owner', 'instance_admin'));

CREATE INDEX orgs_deleted_by_user_id_idx
    ON orgs (deleted_by_user_id)
    WHERE deleted_by_user_id IS NOT NULL;

-- Older deletions used the audit log as their only provenance. Preserve owner
-- restore compatibility when no matching audit record remains.
UPDATE orgs AS o
SET deleted_by_kind = COALESCE((
        SELECT CASE a.action
            WHEN 'instance.organization.deleted' THEN 'instance_admin'
            ELSE 'owner'
        END
        FROM audit_log AS a
        WHERE a.target_type = 'organization'
          AND a.target_id = o.id
          AND a.action IN ('organization.deleted', 'instance.organization.deleted')
        ORDER BY a.id DESC
        LIMIT 1
    ), 'owner'),
    deleted_by_user_id = (
        SELECT a.actor_user_id
        FROM audit_log AS a
        WHERE a.target_type = 'organization'
          AND a.target_id = o.id
          AND a.action IN ('organization.deleted', 'instance.organization.deleted')
        ORDER BY a.id DESC
        LIMIT 1
    )
WHERE o.deleted_at IS NOT NULL;
