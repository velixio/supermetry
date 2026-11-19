package platformhttp

import (
	"net/http"
	"time"

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
	defer h.systemMu.Unlock()
	if !h.systemCachedAt.IsZero() && now.Sub(h.systemCachedAt) < adminSystemCacheTTL && !now.Before(h.systemCachedAt) {
		writeJSON(w, http.StatusOK, h.systemCache)
		return
	}
	info, err := h.deps.Admin.SystemInfo(r.Context())
	if err != nil {
		serviceError(w, err)
		return
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
		if status, err := h.deps.Mail.Status(r.Context()); err == nil {
			response.SMTPConfigured = status.Settings.Host != ""
		}
	}
	if h.deps.UpdateChecker != nil {
		checked := h.deps.UpdateChecker.Check(r.Context())
		response.Update = adminSystemUpdate{CurrentVersion: checked.CurrentVersion, LatestVersion: checked.LatestVersion,
			UpdateAvailable: checked.UpdateAvailable, CheckedAt: checked.CheckedAt, CheckError: checked.CheckError}
	}
	h.systemCache, h.systemCachedAt = response, now
	writeJSON(w, http.StatusOK, response)
}

// diskStatus is nil for an unset path; free_bytes is null when it cannot be read.
func diskStatus(path string) *adminSystemDisk {
	if path == "" {
		return nil
	}
	disk := &adminSystemDisk{Path: path}
	if free, err := diskFreeBytes(path); err == nil {
		disk.FreeBytes = &free
	}
	return disk
}
