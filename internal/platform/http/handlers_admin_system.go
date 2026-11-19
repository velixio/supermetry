package platformhttp

import (
	"context"
	"net/http"
	"time"

	"supermetry/internal/appbackup"
	"supermetry/internal/db"
	"supermetry/internal/health"
	"supermetry/internal/platform/admin"
)

const adminSystemCacheTTL = 15 * time.Second

var processStart = time.Now()

type adminSystemDisk struct {
	Path      string  `json:"path"`
	FreeBytes *uint64 `json:"free_bytes"`
}

type adminSystemIngest struct {
	QueueDepth int64 `json:"queue_depth"`
	Drops503   int64 `json:"drops_503"`
	Accepted   int64 `json:"accepted_total"`
}

type adminSystemUpdate struct {
	CurrentVersion  string     `json:"current_version"`
	LatestVersion   *string    `json:"latest_version"`
	UpdateAvailable bool       `json:"update_available"`
	CheckedAt       *time.Time `json:"checked_at"`
	CheckError      *string    `json:"check_error"`
}

type adminSystemResponse struct {
	Version             string             `json:"version"`
	SchemaVersion       int64              `json:"schema_version"`
	LatestSchemaVersion int64              `json:"latest_schema_version"`
	StartedAt           time.Time          `json:"started_at"`
	UptimeSeconds       int64              `json:"uptime_seconds"`
	Database            admin.DatabaseInfo `json:"database"`
	DataDir             *adminSystemDisk   `json:"data_dir"`
	ReplayDir           *adminSystemDisk   `json:"replay_dir"`
	Ingest              adminSystemIngest  `json:"ingest"`
	Jobs                []admin.JobRun     `json:"jobs"`
	SMTPConfigured      bool               `json:"smtp_configured"`
	Update              adminSystemUpdate  `json:"update"`
	GeneratedAt         time.Time          `json:"generated_at"`
}

// adminSystem reports instance health from cheap sources only, cached for 15 s
// so a dashboard polling it never loads the database.
func (h *handler) adminSystem(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.adminPrincipal(w, r); !ok {
		return
	}
	now := h.deps.Now()
	h.systemMu.Lock()
	if !h.systemCachedAt.IsZero() && now.Sub(h.systemCachedAt) < adminSystemCacheTTL && !now.Before(h.systemCachedAt) {
		cached := h.systemCache
		h.systemMu.Unlock()
		writeJSON(w, http.StatusOK, cached)
		return
	}
	h.systemMu.Unlock()
	// The queries and the update check run outside the lock; concurrent misses
	// share one computation.
	value, err, _ := h.systemFlight.Do("system", func() (any, error) {
		response, err := h.buildAdminSystem(context.WithoutCancel(r.Context()), now)
		if err != nil {
			return nil, err
		}
		h.systemMu.Lock()
		h.systemCache, h.systemCachedAt = response, now
		h.systemMu.Unlock()
		return response, nil
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value.(adminSystemResponse))
}

func (h *handler) buildAdminSystem(ctx context.Context, now time.Time) (adminSystemResponse, error) {
	info, err := h.deps.Admin.SystemInfo(ctx)
	if err != nil {
		return adminSystemResponse{}, err
	}
	response := adminSystemResponse{
		Version:       h.deps.Version,
		SchemaVersion: info.SchemaVersion,
		StartedAt:     processStart.UTC(),
		UptimeSeconds: int64(time.Since(processStart).Seconds()),
		Database:      info.Database,
		Jobs:          info.Jobs,
		GeneratedAt:   now.UTC(),
	}
	if latest, err := db.LatestMigrationVersion(); err == nil {
		response.LatestSchemaVersion = int64(latest)
	}
	response.DataDir = diskStatus(h.deps.DataDir)
	response.ReplayDir = diskStatus(h.deps.ReplayEnv.Dir)
	snapshot := health.Snapshot()
	response.Ingest = adminSystemIngest{
		QueueDepth: int64(snapshot["writer_queue_depth"]),
		Drops503:   int64(snapshot["ingest_dropped_total"]),
		Accepted:   int64(snapshot["ingest_accepted_total"]),
	}
	if h.deps.Mail != nil {
		if status, err := h.deps.Mail.Status(ctx); err == nil {
			response.SMTPConfigured = status.Settings.Host != ""
		}
	}
	if h.deps.UpdateChecker != nil {
		checked := h.deps.UpdateChecker.Check(ctx)
		response.Update = adminSystemUpdate{CurrentVersion: checked.CurrentVersion, LatestVersion: checked.LatestVersion,
			UpdateAvailable: checked.UpdateAvailable, CheckedAt: checked.CheckedAt, CheckError: checked.CheckError}
	}
	return response, nil
}

// diskStatus is nil for an unset path; free_bytes is null when it cannot be read.
func diskStatus(path string) *adminSystemDisk {
	if path == "" {
		return nil
	}
	disk := &adminSystemDisk{Path: path}
	if free, err := appbackup.FreeBytes(path); err == nil {
		disk.FreeBytes = &free
	}
	return disk
}
