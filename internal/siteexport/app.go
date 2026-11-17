package siteexport

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// utc renders a timestamptz column as an RFC 3339 UTC string (” when null).
func utc(column string) string {
	return fmt.Sprintf(`coalesce(to_char(%s at time zone 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),'')`, column)
}

// writeApp adds the App Monitoring files for sites of kind app. It never
// selects user_hash, install or session hashes, event bodies or any IP data:
// each query names its columns explicitly, so a new sensitive column cannot
// leak by default.
func (e *Exporter) writeApp(ctx context.Context, archive *zip.Writer, siteID int32, from, to time.Time) error {
	var kind, tz string
	var orgID int64
	err := e.pool.QueryRow(ctx, `select kind,org_id,timezone from sites where id=$1`, siteID).Scan(&kind, &orgID, &tz)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load site: %w", err)
	}
	if kind != "app" {
		return nil
	}
	// Window in the site's time zone, as a half-open timestamptz range.
	const win = `>= ($2::date)::timestamp at time zone $4 and %s < (($3::date)+1)::timestamp at time zone $4`
	inWindow := func(col string) string { return col + " " + fmt.Sprintf(win, col) }
	f, t := date(from), date(to)

	issues := `
select i.id,i.title,i.culprit,i.level,i.status,i.kind,i.error_type,` + utc("i.first_seen") + `,` + utc("i.last_seen") + `,
 i.event_count,i.first_release,i.last_release,coalesce(i.resolved_in_release,''),
 i.priority,coalesce(i.assignee_user_id::text,''),coalesce(i.github_url,'')
from app_issues i
where i.site_id=$1 and i.last_seen >= ($2::date)::timestamp at time zone $4
  and i.first_seen < (($3::date)+1)::timestamp at time zone $4
order by i.id`
	if err := e.writeRows(ctx, archive, "app_issues.csv", []string{
		"issue_id", "title", "culprit", "level", "status", "kind", "error_type", "first_seen", "last_seen",
		"occurrences", "first_release", "last_release", "resolved_in_release", "priority", "assignee_id", "github_url",
	}, issues, siteID, f, t, tz); err != nil {
		return err
	}

	// Activity and comments are team content, exported as written; actors are
	// organisation user ids.
	activity := `
select a.id,a.issue_id,` + utc("a.at") + `,a.kind,coalesce(a.actor_user_id::text,''),a.data::text
from app_issue_activity a
where a.site_id=$1 and ` + inWindow("a.at") + `
order by a.at,a.id`
	if err := e.writeRows(ctx, archive, "app_issue_activity.csv",
		[]string{"activity_id", "issue_id", "at", "kind", "actor_id", "data"}, activity, siteID, f, t, tz); err != nil {
		return err
	}
	comments := `
select c.id,c.issue_id,coalesce(c.author_user_id::text,''),` + utc("c.created_at") + `,coalesce(` + utc("c.edited_at") + `,''),c.body
from app_issue_comments c
where c.site_id=$1 and c.deleted_at is null and ` + inWindow("c.created_at") + `
order by c.created_at,c.id`
	if err := e.writeRows(ctx, archive, "app_issue_comments.csv",
		[]string{"comment_id", "issue_id", "author_id", "created", "edited", "text"}, comments, siteID, f, t, tz); err != nil {
		return err
	}

	events := `
select v.event_id,` + utc("v.ts") + `,coalesce(v.issue_id::text,''),v.kind,v.level,v.release,v.dist,v.environment,
 v.platform,v.sdk_name,v.sdk_version,v.os_version,v.arch,v.device_class,v.runtime,v.symbolication,v.occurrences
from app_events v
where v.site_id=$1 and ` + inWindow("v.ts") + `
order by v.ts,v.event_id`
	if err := e.writeRows(ctx, archive, "app_events.csv", []string{
		"event_id", "timestamp", "issue_id", "kind", "level", "release", "dist", "environment",
		"platform", "sdk_name", "sdk_version", "os_version", "arch", "device_class", "runtime",
		"symbolication", "occurrences",
	}, events, siteID, f, t, tz); err != nil {
		return err
	}

	// Releases and deploys belong to the organisation; an empty site_ids list
	// means every app project of it.
	releases := `
select r.id,r.version,r.url,` + utc("r.date_created") + `,` + utc("r.date_released") + `
from app_releases r
where r.org_id=$5 and (r.site_ids='{}' or $1=any(r.site_ids)) and ` + inWindow("r.date_created") + `
order by r.id`
	if err := e.writeRows(ctx, archive, "app_releases.csv",
		[]string{"release_id", "version", "url", "created", "released"},
		releases, siteID, f, t, tz, orgID); err != nil {
		return err
	}

	deploys := `
select d.id,r.version,d.environment,d.name,d.url,` + utc("d.date_started") + `,` + utc("d.date_finished") + `,` + utc("d.date_created") + `
from app_deploys d join app_releases r on r.id=d.release_id
where r.org_id=$5 and (r.site_ids='{}' or $1=any(r.site_ids)) and ` + inWindow("d.date_created") + `
order by d.id`
	if err := e.writeRows(ctx, archive, "app_deploys.csv",
		[]string{"deploy_id", "release", "environment", "name", "url", "started", "finished", "created"},
		deploys, siteID, f, t, tz, orgID); err != nil {
		return err
	}

	// Daily counts of events the SDKs report they discarded; no event data.
	reports := `
select c.day::text,c.reason,c.category,c.quantity
from app_client_reports c
where c.site_id=$1 and c.day between $2::date and $3::date
order by c.day,c.reason,c.category`
	if err := e.writeRows(ctx, archive, "app_client_reports.csv",
		[]string{"day", "reason", "category", "quantity"},
		reports, siteID, f, t); err != nil {
		return err
	}

	// Metadata only: never blob keys, file URLs or contents.
	artifacts := `
select b.id,b.release,b.dist,b.state,
 coalesce((select string_agg(distinct x.debug_id::text,' ' order by x.debug_id::text) from app_artifact_files x
           where x.bundle_id=b.id and x.debug_id is not null),''),
 (select count(*) from app_artifact_files x where x.bundle_id=b.id),b.size,` + utc("b.created_at") + `
from app_artifact_bundles b
where b.org_id=$5 and (b.site_ids='{}' or $1=any(b.site_ids)) and ` + inWindow("b.created_at") + `
order by b.id`
	return e.writeRows(ctx, archive, "app_artifacts.csv",
		[]string{"bundle_id", "release", "dist", "state", "debug_ids", "file_count", "size_bytes", "created"},
		artifacts, siteID, f, t, tz, orgID)
}
