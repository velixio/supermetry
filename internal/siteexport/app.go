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

	// Rules are project configuration: exported whole, in evaluation order.
	if err := e.writeRows(ctx, archive, "app_ownership_rules.csv",
		[]string{"position", "matcher", "value", "owner_id"},
		`select position,matcher,value,owner_user_id from app_ownership_rules where site_id=$1 order by position,id`, siteID); err != nil {
		return err
	}
	if err := e.writeRows(ctx, archive, "app_grouping_rules.csv",
		[]string{"position", "matcher", "value", "fingerprint"},
		`select position,matcher,value,fingerprint from app_grouping_rules where site_id=$1 order by position,id`, siteID); err != nil {
		return err
	}
	// Shared saved searches are team content; private ones stay personal.
	views := `
select v.id,v.name,v.query,coalesce(v.owner_user_id::text,''),` + utc("v.created_at") + `
from app_saved_views v
where v.site_id=$1 and v.shared
order by v.id`
	if err := e.writeRows(ctx, archive, "app_saved_views.csv",
		[]string{"view_id", "name", "query", "owner_id", "created"}, views, siteID); err != nil {
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

	// Daily release-health counts; distinct-user counts only, never hashes.
	sessionsDaily := `
select d.day::text,d.release,d.environment,d.platform,d.sessions,d.errored,d.crashed,d.abnormal,d.users,d.crashed_users
from app_session_daily d
where d.site_id=$1 and d.day between $2::date and $3::date
order by d.day,d.release,d.environment,d.platform`
	if err := e.writeRows(ctx, archive, "app_sessions_daily.csv", []string{
		"day", "release", "environment", "platform", "sessions", "errored", "crashed", "abnormal", "users", "crashed_users",
	}, sessionsDaily, siteID, f, t); err != nil {
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

	// User content. The reporter's email is stored only as a hash and is not
	// exported; has_contact says whether one was given.
	feedback := `
select f.id,coalesce(f.issue_id::text,''),coalesce(f.event_id,''),` + utc("f.at") + `,(f.email_hash is not null)::text,f.message,coalesce(f.url,'')
from app_feedback f
where f.site_id=$1 and ` + inWindow("f.at") + `
order by f.at,f.id`
	if err := e.writeRows(ctx, archive, "app_feedback.csv",
		[]string{"feedback_id", "issue_id", "event_id", "at", "has_contact", "text", "page"}, feedback, siteID, f, t, tz); err != nil {
		return err
	}

	deploys := `
select d.id,r.version,d.environment,d.name,d.url,` + utc("d.date_started") + `,` + utc("d.date_finished") + `,` + utc("d.date_created") + `
from app_deploys d join app_releases r on r.id=d.release_id
where d.org_id=$5 and (case when cardinality(d.site_ids)>0 then $1=any(d.site_ids) else (r.site_ids='{}' or $1=any(r.site_ids)) end) and ` + inWindow("d.date_created") + `
order by d.id`
	if err := e.writeRows(ctx, archive, "app_deploys.csv",
		[]string{"deploy_id", "release", "environment", "name", "url", "started", "finished", "created"},
		deploys, siteID, f, t, tz, orgID); err != nil {
		return err
	}

	// Commits of the site's releases (author email is customer-supplied
	// developer metadata; exported, never shown over MCP). A commit shared by
	// two releases appears once per release.
	commits := `
select r.version,p.name,c.sha,c.message,c.author_name,c.author_email,` + utc("c.committed_at") + `
from app_releases r
join app_release_commits rc on rc.release_id=r.id
join app_commits c on c.id=rc.commit_id
join app_repositories p on p.id=c.repository_id
where r.org_id=$5 and (r.site_ids='{}' or $1=any(r.site_ids)) and ` + inWindow("r.date_created") + `
order by r.id,rc.position`
	if err := e.writeRows(ctx, archive, "app_commits.csv",
		[]string{"release", "repository", "sha", "message", "author_name", "author_email", "committed_at"},
		commits, siteID, f, t, tz, orgID); err != nil {
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

	// Active installs per day: counts only, never install ids or their hashes.
	active := `
select g.day::text,
 (select count(*) from app_active_day a where a.site_id=$1 and a.day=g.day),
 (select count(distinct a.visitor_id) from app_active_day a where a.site_id=$1 and a.day between g.day-6 and g.day),
 (select count(distinct a.visitor_id) from app_active_day a where a.site_id=$1 and a.day between g.day-29 and g.day),
 (select count(*) from app_active_day a where a.site_id=$1 and a.day=g.day
    and not exists (select 1 from app_active_day p where p.site_id=$1 and p.visitor_id=a.visitor_id and p.day<a.day))
from (select d::date as day from generate_series($2::date,$3::date,interval '1 day') d) g
where exists (select 1 from app_active_day a where a.site_id=$1 and a.day=g.day)
order by g.day`
	if err := e.writeRows(ctx, archive, "app_active_users.csv",
		[]string{"day", "dau", "wau", "mau", "new_installs"}, active, siteID, f, t); err != nil {
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
	if err := e.writeRows(ctx, archive, "app_artifacts.csv",
		[]string{"bundle_id", "release", "dist", "state", "debug_ids", "file_count", "size_bytes", "created"},
		artifacts, siteID, f, t, tz, orgID); err != nil {
		return err
	}

	// Uploaded debug files: metadata only, never blob keys or contents.
	debugFiles := `
select d.id,d.kind,d.debug_id,d.code_id,d.arch,d.object_name,array_to_string(d.features,' '),d.cache_state,u.size,` + utc("d.created_at") + `
from app_debug_files d
join app_dif_uploads u on u.id=d.upload_id
where d.org_id=$5 and (d.site_id is null or d.site_id=$1) and ` + inWindow("d.created_at") + `
order by d.id`
	return e.writeRows(ctx, archive, "app_debug_files.csv",
		[]string{"file_id", "kind", "debug_id", "code_id", "arch", "object_name", "features", "cache_state", "size_bytes", "created"},
		debugFiles, siteID, f, t, tz, orgID)
}
