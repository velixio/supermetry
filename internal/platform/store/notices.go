package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/platform/lifecycle"
)

type deletionNoticeStore struct {
	pool *pgxpool.Pool
}

type deletionNoticeDB interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func init() {
	lifecycle.RegisterDeletionNoticeStore(func(pool *pgxpool.Pool) lifecycle.DeletionNoticeStore {
		return &deletionNoticeStore{pool: pool}
	})
}

func (s *deletionNoticeStore) PendingDeletionTargets(ctx context.Context, now time.Time) ([]lifecycle.DeletionTarget, error) {
	return PendingDeletionTargets(ctx, s.pool, now)
}

func (s *deletionNoticeStore) RefreshDeletionTarget(ctx context.Context, target lifecycle.DeletionTarget, now time.Time) (lifecycle.DeletionTarget, bool, error) {
	return RefreshDeletionTarget(ctx, s.pool, target, now)
}

func (s *deletionNoticeStore) MarkDeletionTarget(ctx context.Context, target lifecycle.DeletionTarget, stage lifecycle.DeletionStage, at time.Time) (bool, error) {
	return MarkDeletionTarget(ctx, s.pool, target, stage, at)
}

func (s *deletionNoticeStore) RecordDeletionTargetSend(ctx context.Context, target lifecycle.DeletionTarget, stage lifecycle.DeletionStage, userID int64, at time.Time) error {
	return RecordDeletionTargetSend(ctx, s.pool, target, stage, userID, at)
}

func (s *deletionNoticeStore) CompleteDeletionTarget(ctx context.Context, target lifecycle.DeletionTarget, stage lifecycle.DeletionStage, at time.Time) (bool, error) {
	return CompleteDeletionTarget(ctx, s.pool, target, stage, at)
}

// PendingDeletionTargets returns live deletion lifecycles that still need a
// scheduled notice or reminder.
func PendingDeletionTargets(ctx context.Context, db deletionNoticeDB, now time.Time) ([]lifecycle.DeletionTarget, error) {
	sites, err := pendingSiteDeletionTargets(ctx, db, now, nil)
	if err != nil {
		return nil, err
	}
	orgs, err := pendingOrgDeletionTargets(ctx, db, now, nil)
	if err != nil {
		return nil, err
	}
	return append(sites, orgs...), nil
}

// RefreshDeletionTarget rechecks lifecycle state and current recipients just
// before delivery. It rejects a restored or replaced deletion lifecycle.
func RefreshDeletionTarget(ctx context.Context, db deletionNoticeDB, target lifecycle.DeletionTarget, now time.Time) (lifecycle.DeletionTarget, bool, error) {
	id := target.ID
	var targets []lifecycle.DeletionTarget
	var err error
	switch target.Kind {
	case lifecycle.DeletionSite:
		targets, err = pendingSiteDeletionTargets(ctx, db, now, &id)
	case lifecycle.DeletionOrg:
		targets, err = pendingOrgDeletionTargets(ctx, db, now, &id)
	default:
		return lifecycle.DeletionTarget{}, false, fmt.Errorf("invalid deletion target kind %d", target.Kind)
	}
	if err != nil || len(targets) == 0 || !targets[0].DeletedAt.Equal(target.DeletedAt) {
		return lifecycle.DeletionTarget{}, false, err
	}
	return targets[0], true, nil
}

func pendingSiteDeletionTargets(ctx context.Context, db deletionNoticeDB, now time.Time, id *int64) ([]lifecycle.DeletionTarget, error) {
	var selectedID any
	if id != nil {
		selectedID = *id
	}
	rows, err := db.Query(ctx, `
		select s.id::bigint,s.name,o.slug,s.deleted_at,
		       s.deletion_notified_at,s.deletion_reminded_at,
		       coalesce(array_agg(u.id order by m.user_id) filter (where u.id is not null), '{}'::bigint[]),
		       coalesce(array_agg(u.email order by m.user_id) filter (where u.id is not null), '{}'::text[]),
		       coalesce((select array_agg(d.user_id order by d.user_id) from deletion_notice_sends d where d.target_type='site' and d.target_id=s.id and d.kind='scheduled' and d.deleted_at=s.deleted_at), '{}'::bigint[]),
		       coalesce((select array_agg(d.user_id order by d.user_id) from deletion_notice_sends d where d.target_type='site' and d.target_id=s.id and d.kind='reminder' and d.deleted_at=s.deleted_at), '{}'::bigint[])
		from sites s
		join orgs o on o.id=s.org_id
		left join memberships m on m.org_id=s.org_id and m.role in ('owner','admin')
		left join users u on u.id=m.user_id and u.deleted_at is null
		where s.deleted_at is not null
		  and s.deleted_at > $1::timestamptz - interval '7 days'
		  and s.erased_at is null
		  and not s.is_demo
		  and not s.deleted_with_org
		  and o.deleted_at is null
		  and ($2::bigint is null or s.id=$2)
		  and (s.deletion_notified_at is null or s.deletion_reminded_at is null)
		group by s.id,s.name,o.slug,s.deleted_at,s.deletion_notified_at,s.deletion_reminded_at
		order by s.id`, now, selectedID)
	if err != nil {
		return nil, fmt.Errorf("query pending site deletion notices: %w", err)
	}
	defer rows.Close()
	targets := make([]lifecycle.DeletionTarget, 0)
	for rows.Next() {
		var target lifecycle.DeletionTarget
		target.Kind = lifecycle.DeletionSite
		if err := rows.Scan(
			&target.ID, &target.Name, &target.OrgSlug, &target.DeletedAt,
			&target.NotifiedAt, &target.RemindedAt, &target.RecipientIDs, &target.Recipients,
			&target.NotifiedRecipientIDs, &target.RemindedRecipientIDs,
		); err != nil {
			return nil, fmt.Errorf("scan pending site deletion notice: %w", err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending site deletion notices: %w", err)
	}
	return targets, nil
}

func pendingOrgDeletionTargets(ctx context.Context, db deletionNoticeDB, now time.Time, id *int64) ([]lifecycle.DeletionTarget, error) {
	var selectedID any
	if id != nil {
		selectedID = *id
	}
	rows, err := db.Query(ctx, `
		select o.id,o.name,o.slug,o.deleted_at,
		       o.deletion_notified_at,o.deletion_reminded_at,
		       coalesce(recipients.user_ids, '{}'::bigint[]),coalesce(recipients.emails, '{}'::text[]),
		       coalesce((select array_agg(d.user_id order by d.user_id) from deletion_notice_sends d where d.target_type='org' and d.target_id=o.id and d.kind='scheduled' and d.deleted_at=o.deleted_at), '{}'::bigint[]),
		       coalesce((select array_agg(d.user_id order by d.user_id) from deletion_notice_sends d where d.target_type='org' and d.target_id=o.id and d.kind='reminder' and d.deleted_at=o.deleted_at), '{}'::bigint[]),
		       org_sites.names
		from orgs o
		cross join lateral (
			select array_agg(s.name order by s.id) as names
			from sites s
			where s.org_id=o.id
			  and not s.is_demo
			  and s.erased_at is null
		) org_sites
		left join lateral (
			select array_agg(u.id order by m.user_id) as user_ids,array_agg(u.email order by m.user_id) as emails
			from memberships m
			join users u on u.id=m.user_id and u.deleted_at is null
			where m.org_id=o.id and m.role='owner'
		) recipients on true
		where o.deleted_at is not null
		  and o.deleted_at > $1::timestamptz - interval '7 days'
		  and o.erased_at is null
		  and org_sites.names is not null
		  and ($2::bigint is null or o.id=$2)
		  and (o.deletion_notified_at is null or o.deletion_reminded_at is null)
		order by o.id`, now, selectedID)
	if err != nil {
		return nil, fmt.Errorf("query pending organization deletion notices: %w", err)
	}
	defer rows.Close()
	targets := make([]lifecycle.DeletionTarget, 0)
	for rows.Next() {
		var target lifecycle.DeletionTarget
		target.Kind = lifecycle.DeletionOrg
		if err := rows.Scan(
			&target.ID, &target.Name, &target.OrgSlug, &target.DeletedAt,
			&target.NotifiedAt, &target.RemindedAt, &target.RecipientIDs, &target.Recipients,
			&target.NotifiedRecipientIDs, &target.RemindedRecipientIDs, &target.Items,
		); err != nil {
			return nil, fmt.Errorf("scan pending organization deletion notice: %w", err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending organization deletion notices: %w", err)
	}
	return targets, nil
}

// MarkDeletionTarget records a successful delivery only when the target still
// belongs to the same deletion lifecycle selected for the send.
func MarkDeletionTarget(ctx context.Context, db deletionNoticeDB, target lifecycle.DeletionTarget, stage lifecycle.DeletionStage, at time.Time) (bool, error) {
	var query string
	switch {
	case target.Kind == lifecycle.DeletionSite && stage == lifecycle.DeletionNotified:
		query = `update sites set deletion_notified_at=$3 where id=$1 and deleted_at=$2 and erased_at is null and deletion_notified_at is null`
	case target.Kind == lifecycle.DeletionSite && stage == lifecycle.DeletionReminded:
		query = `update sites set deletion_reminded_at=$3 where id=$1 and deleted_at=$2 and erased_at is null and deletion_reminded_at is null`
	case target.Kind == lifecycle.DeletionOrg && stage == lifecycle.DeletionNotified:
		query = `update orgs set deletion_notified_at=$3 where id=$1 and deleted_at=$2 and erased_at is null and deletion_notified_at is null`
	case target.Kind == lifecycle.DeletionOrg && stage == lifecycle.DeletionReminded:
		query = `update orgs set deletion_reminded_at=$3 where id=$1 and deleted_at=$2 and erased_at is null and deletion_reminded_at is null`
	default:
		return false, fmt.Errorf("invalid deletion target kind %d or stage %d", target.Kind, stage)
	}
	tag, err := db.Exec(ctx, query, target.ID, target.DeletedAt, at)
	if err != nil {
		return false, fmt.Errorf("mark deletion target kind %d id %d stage %d: %w", target.Kind, target.ID, stage, err)
	}
	return tag.RowsAffected() == 1, nil
}

// RecordDeletionTargetSend records one successful recipient delivery for the
// target's current deletion lifecycle.
func RecordDeletionTargetSend(ctx context.Context, db deletionNoticeDB, target lifecycle.DeletionTarget, stage lifecycle.DeletionStage, userID int64, at time.Time) error {
	targetType, kind, err := deletionSendKeys(target.Kind, stage)
	if err != nil {
		return err
	}
	var query string
	switch target.Kind {
	case lifecycle.DeletionSite:
		query = `
			insert into deletion_notice_sends(target_type,target_id,kind,deleted_at,user_id,sent_at)
			select $1,$2::bigint,$3,$4,$5,$6 from sites
			where id=$2::integer and deleted_at=$4 and erased_at is null
			on conflict do nothing`
	case lifecycle.DeletionOrg:
		query = `
			insert into deletion_notice_sends(target_type,target_id,kind,deleted_at,user_id,sent_at)
			select $1,$2,$3,$4,$5,$6 from orgs
			where id=$2 and deleted_at=$4 and erased_at is null
			on conflict do nothing`
	}
	if _, err := db.Exec(ctx, query, targetType, target.ID, kind, target.DeletedAt, userID, at); err != nil {
		return fmt.Errorf("record deletion notice send kind %d id %d stage %d: %w", target.Kind, target.ID, stage, err)
	}
	return nil
}

// CompleteDeletionTarget marks a stage only after every active current
// recipient has a successful-send row for the same deletion lifecycle.
func CompleteDeletionTarget(ctx context.Context, db deletionNoticeDB, target lifecycle.DeletionTarget, stage lifecycle.DeletionStage, at time.Time) (bool, error) {
	targetType, kind, err := deletionSendKeys(target.Kind, stage)
	if err != nil {
		return false, err
	}
	var query string
	switch {
	case target.Kind == lifecycle.DeletionSite && stage == lifecycle.DeletionNotified:
		query = completeSiteDeletionTargetQuery("deletion_notified_at")
	case target.Kind == lifecycle.DeletionSite && stage == lifecycle.DeletionReminded:
		query = completeSiteDeletionTargetQuery("deletion_reminded_at")
	case target.Kind == lifecycle.DeletionOrg && stage == lifecycle.DeletionNotified:
		query = completeOrgDeletionTargetQuery("deletion_notified_at")
	case target.Kind == lifecycle.DeletionOrg && stage == lifecycle.DeletionReminded:
		query = completeOrgDeletionTargetQuery("deletion_reminded_at")
	default:
		return false, fmt.Errorf("invalid deletion target kind %d or stage %d", target.Kind, stage)
	}
	tag, err := db.Exec(ctx, query, target.ID, target.DeletedAt, at, targetType, kind)
	if err != nil {
		return false, fmt.Errorf("complete deletion target kind %d id %d stage %d: %w", target.Kind, target.ID, stage, err)
	}
	return tag.RowsAffected() == 1, nil
}

func deletionSendKeys(target lifecycle.DeletionKind, stage lifecycle.DeletionStage) (string, string, error) {
	var targetType string
	switch target {
	case lifecycle.DeletionSite:
		targetType = "site"
	case lifecycle.DeletionOrg:
		targetType = "org"
	default:
		return "", "", fmt.Errorf("invalid deletion target kind %d", target)
	}
	var kind string
	switch stage {
	case lifecycle.DeletionNotified:
		kind = "scheduled"
	case lifecycle.DeletionReminded:
		kind = "reminder"
	default:
		return "", "", fmt.Errorf("invalid deletion stage %d", stage)
	}
	return targetType, kind, nil
}

func completeSiteDeletionTargetQuery(column string) string {
	return fmt.Sprintf(`
		update sites s set %s=$3
		where s.id=$1 and s.deleted_at=$2 and s.erased_at is null and s.%s is null
		  and exists (
			select 1 from memberships m join users u on u.id=m.user_id and u.deleted_at is null
			where m.org_id=s.org_id and m.role in ('owner','admin')
		  )
		  and not exists (
			select 1 from memberships m join users u on u.id=m.user_id and u.deleted_at is null
			where m.org_id=s.org_id and m.role in ('owner','admin')
			  and not exists (
				select 1 from deletion_notice_sends d
				where d.target_type=$4 and d.target_id=s.id and d.kind=$5
				  and d.deleted_at=s.deleted_at and d.user_id=m.user_id
			  )
		  )`, column, column)
}

func completeOrgDeletionTargetQuery(column string) string {
	return fmt.Sprintf(`
		update orgs o set %s=$3
		where o.id=$1 and o.deleted_at=$2 and o.erased_at is null and o.%s is null
		  and exists (
			select 1 from memberships m join users u on u.id=m.user_id and u.deleted_at is null
			where m.org_id=o.id and m.role='owner'
		  )
		  and not exists (
			select 1 from memberships m join users u on u.id=m.user_id and u.deleted_at is null
			where m.org_id=o.id and m.role='owner'
			  and not exists (
				select 1 from deletion_notice_sends d
				where d.target_type=$4 and d.target_id=o.id and d.kind=$5
				  and d.deleted_at=o.deleted_at and d.user_id=m.user_id
			  )
		  )`, column, column)
}
