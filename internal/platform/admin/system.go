package admin

import (
	"context"
	"time"
)

// DatabaseInfo is the cheap database health the system page shows.
type DatabaseInfo struct {
	SizeBytes      int64 `json:"size_bytes"`
	Connections    int   `json:"connections"`
	MaxConnections int   `json:"max_connections"`
}

// JobRun is the last recorded run of a background job; LastRunAt is nil when it never ran.
type JobRun struct {
	Name      string     `json:"name"`
	LastRunAt *time.Time `json:"last_run_at"`
}

// SystemInfo is the database-side part of the system status.
type SystemInfo struct {
	SchemaVersion int64
	Database      DatabaseInfo
	Jobs          []JobRun
}

// SystemInfo runs a handful of catalog and last-run lookups; none scans data tables.
func (s *Service) SystemInfo(ctx context.Context) (SystemInfo, error) {
	var info SystemInfo
	if err := s.store.Pool.QueryRow(ctx, `
		select coalesce((select max(version) from schema_migrations),0),
		       pg_database_size(current_database()),
		       (select count(*) from pg_stat_activity where datname=current_database()),
		       current_setting('max_connections')::int`).
		Scan(&info.SchemaVersion, &info.Database.SizeBytes, &info.Database.Connections, &info.Database.MaxConnections); err != nil {
		return SystemInfo{}, err
	}
	jobs := []struct{ name, sql string }{
		{"storage_usage", `select max(completed_at) from storage_usage_runs where status='complete'`},
		{"export_schedules", `select max(last_run_at) from export_schedules`},
		{"github_trending", `select max(last_run_at) from github_trending_run`},
		{"instance_update", `select max(finished_at) from instance_update_jobs`},
	}
	for _, job := range jobs {
		var last *time.Time
		if err := s.store.Pool.QueryRow(ctx, job.sql).Scan(&last); err != nil {
			return SystemInfo{}, err
		}
		info.Jobs = append(info.Jobs, JobRun{Name: job.name, LastRunAt: last})
	}
	return info, nil
}
