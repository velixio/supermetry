package appquery

import (
	"context"
	"time"
)

// maxDeploys bounds one deploy-marker read.
const maxDeploys = 500

// Deploy is one deployment of a release, placed on the time axis by At: the
// finish time, else the start time, else the time it was recorded.
type Deploy struct {
	ID          int64     `json:"id"`
	Release     string    `json:"release"`
	Environment string    `json:"environment"`
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	At          time.Time `json:"at"`
}

// Deploys lists the deploys that apply to the site and fall inside the range
// (site time zone, clamped to retention), newest first. A deploy applies to
// the projects it names; one naming none applies to its release's projects,
// and an org-wide release applies to every project of the org. Filter
// environment and an exact release narrow the list.
func (q *Querier) Deploys(ctx context.Context, scope Scope, f Filter, release string) ([]Deploy, error) {
	w, err := q.window(scope, f)
	if err != nil {
		return nil, err
	}
	rows, err := q.pool.Query(ctx, `
select d.id, r.version, d.environment, d.name, d.url, d.at
from app_deploys d
join app_releases r on r.id = d.release_id
where d.org_id = $1
  and d.at >= $2 and d.at < $3
  and ($4 = '' or d.environment = $4)
  and ($5 = '' or r.version = $5)
  and (case when cardinality(d.site_ids) > 0 then $6 = any(d.site_ids)
            when cardinality(r.site_ids) > 0 then $6 = any(r.site_ids)
            else true end)
order by d.at desc, d.id desc
limit $7`, scope.OrgID, w.start, w.end, f.Environment, release, scope.SiteID, maxDeploys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deploy{}
	for rows.Next() {
		var d Deploy
		if err := rows.Scan(&d.ID, &d.Release, &d.Environment, &d.Name, &d.URL, &d.At); err != nil {
			return nil, err
		}
		d.At = d.At.UTC()
		out = append(out, d)
	}
	return out, rows.Err()
}
