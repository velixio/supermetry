package platformhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/exportschedule"
	"supermetry/internal/orgbundle"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
)

// Organization-level export and portability: scheduled exports to S3-compatible
// storage and the settings bundle. Only organization owners and admins
// (authz.ActionOrgUpdate) may use them.

// maxBundleBytes caps a settings bundle upload.
const maxBundleBytes = 8 << 20

func (h *handler) exportSchedules(w http.ResponseWriter, r *http.Request) (*exportschedule.Service, authz.Principal, bool) {
	principal, ok := h.orgManager(w, r, authz.ActionOrgUpdate)
	if !ok {
		return nil, authz.Principal{}, false
	}
	if h.deps.ExportSchedules == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "scheduled exports are unavailable")
		return nil, authz.Principal{}, false
	}
	return h.deps.ExportSchedules, principal, true
}

func exportScheduleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, exportschedule.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "export schedule not found")
	case errors.Is(err, exportschedule.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	default:
		serviceError(w, err)
	}
}

func (h *handler) auditOrg(ctx context.Context, principal authz.Principal, action, targetType string, targetID int64, meta map[string]any) error {
	if h.deps.Store == nil {
		return nil
	}
	return h.deps.Store.Tx(ctx, func(tx pgx.Tx) error {
		return audit.Write(ctx, tx, principal, action, targetType, targetID, meta)
	})
}

func scheduleID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := parseInt64(r.PathValue("schedule"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "schedule must be a positive integer")
		return 0, false
	}
	return id, true
}

type exportScheduleBody struct {
	Name        string `json:"name"`
	SiteID      *int32 `json:"site_id"`
	Frequency   string `json:"frequency"`
	Incremental *bool  `json:"incremental"`
	Endpoint    string `json:"endpoint"`
	Region      string `json:"region"`
	Bucket      string `json:"bucket"`
	Prefix      string `json:"prefix"`
	AccessKeyID string `json:"access_key_id"`
	Secret      string `json:"secret"`
	PathStyle   bool   `json:"path_style"`
}

func (h *handler) listExportSchedules(w http.ResponseWriter, r *http.Request) {
	svc, principal, ok := h.exportSchedules(w, r)
	if !ok {
		return
	}
	items, err := svc.List(r.Context(), principal.OrgID)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": items})
}

func (h *handler) createExportSchedule(w http.ResponseWriter, r *http.Request) {
	svc, principal, ok := h.exportSchedules(w, r)
	if !ok {
		return
	}
	var body exportScheduleBody
	if !decodeJSON(w, r, &body) {
		return
	}
	incremental := body.Incremental == nil || *body.Incremental
	created, err := svc.Create(r.Context(), principal.OrgID, principal.UserID, exportschedule.Input{
		Name: body.Name, SiteID: body.SiteID, Frequency: body.Frequency, Incremental: incremental, Endpoint: body.Endpoint,
		Region: body.Region, Bucket: body.Bucket, Prefix: body.Prefix, AccessKeyID: body.AccessKeyID, Secret: body.Secret, PathStyle: body.PathStyle,
	})
	if err != nil {
		exportScheduleError(w, err)
		return
	}
	if err := h.auditOrg(r.Context(), principal, "export_schedule.created", "export_schedule", created.ID, map[string]any{"frequency": created.Frequency, "bucket": created.Bucket}); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

type exportSchedulePatch struct {
	Name        *string `json:"name"`
	Frequency   *string `json:"frequency"`
	Incremental *bool   `json:"incremental"`
	Enabled     *bool   `json:"enabled"`
	Endpoint    *string `json:"endpoint"`
	Region      *string `json:"region"`
	Bucket      *string `json:"bucket"`
	Prefix      *string `json:"prefix"`
	AccessKeyID *string `json:"access_key_id"`
	Secret      *string `json:"secret"`
	PathStyle   *bool   `json:"path_style"`
}

func (h *handler) updateExportSchedule(w http.ResponseWriter, r *http.Request) {
	svc, principal, ok := h.exportSchedules(w, r)
	if !ok {
		return
	}
	id, ok := scheduleID(w, r)
	if !ok {
		return
	}
	var body exportSchedulePatch
	if !decodeJSON(w, r, &body) {
		return
	}
	updated, err := svc.Update(r.Context(), principal.OrgID, id, exportschedule.Patch(body))
	if err != nil {
		exportScheduleError(w, err)
		return
	}
	if err := h.auditOrg(r.Context(), principal, "export_schedule.updated", "export_schedule", id, map[string]any{"secret_changed": body.Secret != nil && *body.Secret != ""}); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *handler) deleteExportSchedule(w http.ResponseWriter, r *http.Request) {
	svc, principal, ok := h.exportSchedules(w, r)
	if !ok {
		return
	}
	id, ok := scheduleID(w, r)
	if !ok {
		return
	}
	if err := svc.Delete(r.Context(), principal.OrgID, id); err != nil {
		exportScheduleError(w, err)
		return
	}
	if err := h.auditOrg(r.Context(), principal, "export_schedule.deleted", "export_schedule", id, nil); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) runExportSchedule(w http.ResponseWriter, r *http.Request) {
	svc, principal, ok := h.exportSchedules(w, r)
	if !ok {
		return
	}
	id, ok := scheduleID(w, r)
	if !ok {
		return
	}
	if err := svc.RunNow(r.Context(), principal.OrgID, id); err != nil {
		exportScheduleError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "queued"})
}

func (h *handler) testExportSchedule(w http.ResponseWriter, r *http.Request) {
	svc, principal, ok := h.exportSchedules(w, r)
	if !ok {
		return
	}
	id, ok := scheduleID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	err := svc.Test(ctx, principal.OrgID, id)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case errors.Is(err, exportschedule.ErrNotFound):
		exportScheduleError(w, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
	}
}

func (h *handler) listExportRuns(w http.ResponseWriter, r *http.Request) {
	svc, principal, ok := h.exportSchedules(w, r)
	if !ok {
		return
	}
	id, ok := scheduleID(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := svc.Runs(r.Context(), principal.OrgID, id, limit)
	if err != nil {
		exportScheduleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (h *handler) orgBundleService(w http.ResponseWriter, r *http.Request) (*orgbundle.Service, authz.Principal, bool) {
	principal, ok := h.orgManager(w, r, authz.ActionOrgUpdate)
	if !ok {
		return nil, authz.Principal{}, false
	}
	if h.deps.OrgBundle == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "settings export is unavailable")
		return nil, authz.Principal{}, false
	}
	return h.deps.OrgBundle, principal, true
}

func (h *handler) exportOrgSettings(w http.ResponseWriter, r *http.Request) {
	svc, principal, ok := h.orgBundleService(w, r)
	if !ok {
		return
	}
	bundle, err := svc.Export(r.Context(), principal)
	if err != nil {
		serviceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="supermetry-settings-%s.json"`, bundle.Org.Slug))
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(bundle)
}

func (h *handler) importOrgSettings(w http.ResponseWriter, r *http.Request) {
	svc, principal, ok := h.orgBundleService(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	opt := orgbundle.ImportOptions{DryRun: query.Get("dry_run") == "true", InviteMembers: query.Get("invite_members") == "true"}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBundleBytes))
	if err != nil {
		writeProblem(w, http.StatusRequestEntityTooLarge, "Request entity too large", "the settings bundle is too large")
		return
	}
	var bundle orgbundle.Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "the body is not a settings bundle")
		return
	}
	report, err := svc.Import(r.Context(), principal, bundle, opt)
	switch {
	case errors.Is(err, orgbundle.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	case err != nil:
		serviceError(w, err)
	default:
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, report)
	}
}
