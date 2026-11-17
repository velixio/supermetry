package storageusage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ArtifactScanner attributes uploaded source map storage to sites. The bytes
// are the assembled bundle sizes the organization cap is enforced against
// (app_artifact_bundles.size), reported in the auxiliary category next to the
// bundles' index rows. A bundle that names sites is split between them, and
// one that covers the whole organization is split between its live app sites,
// so the per-site numbers add up to the bundle size. Chunk uploads are
// transient, content-addressed and not recorded per organization, so they are
// not counted.
type ArtifactScanner struct{ pool *pgxpool.Pool }

func NewArtifactScanner(pool *pgxpool.Pool) *ArtifactScanner { return &ArtifactScanner{pool: pool} }

func (s *ArtifactScanner) Fingerprint() string { return "app-artifact-bundle-bytes-v1" }

func (s *ArtifactScanner) WorkUnits(ctx context.Context, runID int64) ([]WorkUnit, error) {
	rows, err := s.pool.Query(ctx, `
		select distinct b.org_id
		from app_artifact_bundles b
		join sites s on s.org_id=b.org_id
		join storage_usage_run_sites rs on rs.site_id=s.id and rs.run_id=$1
		where b.state='ok' and b.size>0
		order by b.org_id`, runID)
	if err != nil {
		return nil, fmt.Errorf("list source map storage organizations: %w", err)
	}
	defer rows.Close()
	var units []WorkUnit
	for rows.Next() {
		var orgID int64
		if err := rows.Scan(&orgID); err != nil {
			return nil, fmt.Errorf("scan source map storage organization: %w", err)
		}
		units = append(units, artifactWorkUnit(orgID))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source map storage organizations: %w", err)
	}
	return units, nil
}

func (s *ArtifactScanner) Scan(ctx context.Context, runID int64, unit WorkUnit) ([]Measurement, error) {
	expected := artifactWorkUnit(unit.OrgID)
	expected.Scanner = unit.Scanner
	if unit != expected {
		return nil, fmt.Errorf("invalid source map storage work unit %q", unit.Key)
	}
	// One statement, so the captured-site and erasure checks share a snapshot
	// with the sizes. The remainder of an uneven split goes to the lowest ids.
	rows, err := s.pool.Query(ctx, `
		with covered as (
			select b.size, s.id as site_id,
				row_number() over (partition by b.id order by s.id) as rn,
				count(*) over (partition by b.id) as cnt
			from app_artifact_bundles b
			join sites s on s.org_id=b.org_id
				and s.erased_at is null and s.erasure_started_at is null
				and (s.id = any(b.site_ids) or (cardinality(b.site_ids)=0 and s.kind='app'))
			where b.org_id=$2 and b.state='ok' and b.size>0
		)
		select c.site_id, sum(c.size/c.cnt + case when c.rn <= c.size % c.cnt then 1 else 0 end)::bigint
		from covered c
		join storage_usage_run_sites rs on rs.run_id=$1 and rs.site_id=c.site_id
		join storage_usage_runs r on r.id=rs.run_id and r.status='active'
		group by c.site_id
		order by c.site_id`, runID, unit.OrgID)
	if err != nil {
		return nil, fmt.Errorf("measure source map storage for organization %d: %w", unit.OrgID, err)
	}
	defer rows.Close()
	var values []Contribution
	for rows.Next() {
		var v Contribution
		if err := rows.Scan(&v.SiteID, &v.Bytes); err != nil {
			return nil, fmt.Errorf("scan source map storage: %w", err)
		}
		values = append(values, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source map storage: %w", err)
	}
	if len(values) == 0 {
		return nil, nil
	}
	return []Measurement{{Category: CategoryAuxiliary, Values: values}}, nil
}

func artifactWorkUnit(orgID int64) WorkUnit {
	if orgID < 1 {
		return WorkUnit{}
	}
	return WorkUnit{Key: fmt.Sprintf("artifacts:%010d", orgID), OrgID: orgID}
}

var _ Scanner = (*ArtifactScanner)(nil)
