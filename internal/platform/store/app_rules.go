package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/apprules"
)

// AppOwnershipRulesBySite returns every project's ownership rules in position order.
func (s *Store) AppOwnershipRulesBySite(ctx context.Context) (map[int32][]apprules.OwnershipRule, error) {
	rows, err := s.Pool.Query(ctx, `select r.site_id, r.matcher, r.value, r.owner_user_id
from app_ownership_rules r join sites s on s.id = r.site_id
where s.deleted_at is null order by r.site_id, r.position, r.id`)
	if err != nil {
		return nil, fmt.Errorf("load ownership rules: %w", err)
	}
	defer rows.Close()
	out := map[int32][]apprules.OwnershipRule{}
	for rows.Next() {
		var site int32
		var r apprules.OwnershipRule
		if err := rows.Scan(&site, &r.Matcher, &r.Value, &r.OwnerUserID); err != nil {
			return nil, fmt.Errorf("scan ownership rule: %w", err)
		}
		out[site] = append(out[site], r)
	}
	return out, rows.Err()
}

// AppGroupingRulesBySite returns every project's grouping rules in position order.
func (s *Store) AppGroupingRulesBySite(ctx context.Context) (map[int32][]apprules.GroupingRule, error) {
	rows, err := s.Pool.Query(ctx, `select r.site_id, r.matcher, r.value, r.fingerprint
from app_grouping_rules r join sites s on s.id = r.site_id
where s.deleted_at is null order by r.site_id, r.position, r.id`)
	if err != nil {
		return nil, fmt.Errorf("load grouping rules: %w", err)
	}
	defer rows.Close()
	out := map[int32][]apprules.GroupingRule{}
	for rows.Next() {
		var site int32
		var r apprules.GroupingRule
		if err := rows.Scan(&site, &r.Matcher, &r.Value, &r.Fingerprint); err != nil {
			return nil, fmt.Errorf("scan grouping rule: %w", err)
		}
		out[site] = append(out[site], r)
	}
	return out, rows.Err()
}

// AppRulesForSite returns one project's rules for the settings page.
func (s *Store) AppRulesForSite(ctx context.Context, siteID int32) ([]apprules.OwnershipRule, []apprules.GroupingRule, error) {
	own := []apprules.OwnershipRule{}
	rows, err := s.Pool.Query(ctx, `select matcher, value, owner_user_id from app_ownership_rules where site_id = $1 order by position, id`, siteID)
	if err != nil {
		return nil, nil, fmt.Errorf("load ownership rules: %w", err)
	}
	for rows.Next() {
		var r apprules.OwnershipRule
		if err := rows.Scan(&r.Matcher, &r.Value, &r.OwnerUserID); err != nil {
			rows.Close()
			return nil, nil, err
		}
		own = append(own, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	grp := []apprules.GroupingRule{}
	rows, err = s.Pool.Query(ctx, `select matcher, value, fingerprint from app_grouping_rules where site_id = $1 order by position, id`, siteID)
	if err != nil {
		return nil, nil, fmt.Errorf("load grouping rules: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r apprules.GroupingRule
		if err := rows.Scan(&r.Matcher, &r.Value, &r.Fingerprint); err != nil {
			return nil, nil, err
		}
		grp = append(grp, r)
	}
	return own, grp, rows.Err()
}

// OwnersInSite returns the ids among users that belong to the site's
// organisation with access to the site.
func (s *Store) OwnersInSite(ctx context.Context, siteID int32, users []int64) (map[int64]bool, error) {
	rows, err := s.Pool.Query(ctx, `select m.user_id from memberships m join sites s on s.org_id = m.org_id
where s.id = $1 and m.user_id = any($2) and (m.site_ids is null or $1 = any(m.site_ids))`, siteID, users)
	if err != nil {
		return nil, fmt.Errorf("check rule owners: %w", err)
	}
	defer rows.Close()
	ok := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ok[id] = true
	}
	return ok, rows.Err()
}

// ReplaceAppRules replaces both rule lists of a project inside the caller's transaction.
func (s *Store) ReplaceAppRules(ctx context.Context, tx pgx.Tx, siteID int32, own []apprules.OwnershipRule, grp []apprules.GroupingRule) error {
	if _, err := tx.Exec(ctx, `delete from app_ownership_rules where site_id = $1`, siteID); err != nil {
		return fmt.Errorf("clear ownership rules: %w", err)
	}
	if _, err := tx.Exec(ctx, `delete from app_grouping_rules where site_id = $1`, siteID); err != nil {
		return fmt.Errorf("clear grouping rules: %w", err)
	}
	b := &pgx.Batch{}
	for i, r := range own {
		b.Queue(`insert into app_ownership_rules (site_id, position, matcher, value, owner_user_id) values ($1,$2,$3,$4,$5)`, siteID, i, r.Matcher, r.Value, r.OwnerUserID)
	}
	for i, r := range grp {
		b.Queue(`insert into app_grouping_rules (site_id, position, matcher, value, fingerprint) values ($1,$2,$3,$4,$5)`, siteID, i, r.Matcher, r.Value, r.Fingerprint)
	}
	if b.Len() == 0 {
		return nil
	}
	res := tx.SendBatch(ctx, b)
	for i := 0; i < b.Len(); i++ {
		if _, err := res.Exec(); err != nil {
			_ = res.Close()
			return fmt.Errorf("save rule: %w", err)
		}
	}
	return res.Close()
}
