package platformhttp

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"panel/internal/imports"
	"panel/internal/platform/audit"
	"panel/internal/platform/authz"
)

var errImportTooLarge = errors.New("import file exceeds size limit")

const maxImportBytes int64 = 512 << 20

type importUpload struct {
	Source   string
	FileName string
	Path     string
	FileHash [sha256.Size]byte
}

func copyUpload(dst io.Writer, src io.Reader, max int64) error {
	if _, err := io.Copy(dst, io.LimitReader(src, max)); err != nil {
		return err
	}
	var extra [1]byte
	n, err := src.Read(extra[:])
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if n != 0 {
		return errImportTooLarge
	}
	return nil
}

func saveImportMultipart(r *http.Request, tempDir string, max int64) (upload importUpload, resultErr error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return upload, errors.New("request must be multipart/form-data")
	}
	defer func() {
		if resultErr != nil && upload.Path != "" {
			_ = os.Remove(upload.Path)
		}
	}()
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return upload, fmt.Errorf("read multipart upload: %w", err)
		}
		switch part.FormName() {
		case "source":
			value, err := io.ReadAll(io.LimitReader(part, 33))
			if err != nil {
				part.Close()
				return upload, fmt.Errorf("read import source: %w", err)
			}
			upload.Source = strings.TrimSpace(string(value))
		case "file":
			if upload.Path != "" || part.FileName() == "" {
				part.Close()
				return upload, errors.New("exactly one import file is required")
			}
			file, err := os.CreateTemp(tempDir, "panel-import-*")
			if err != nil {
				part.Close()
				return upload, fmt.Errorf("create import temp file: %w", err)
			}
			upload.Path = file.Name()
			upload.FileName = filepath.Base(part.FileName())
			hasher := sha256.New()
			copyErr := copyUpload(io.MultiWriter(file, hasher), part, max)
			closeErr := file.Close()
			if copyErr != nil {
				part.Close()
				return upload, copyErr
			}
			if closeErr != nil {
				part.Close()
				return upload, fmt.Errorf("close import temp file: %w", closeErr)
			}
			copy(upload.FileHash[:], hasher.Sum(nil))
		default:
			if _, err := io.Copy(io.Discard, part); err != nil {
				part.Close()
				return upload, fmt.Errorf("discard multipart field: %w", err)
			}
		}
		if err := part.Close(); err != nil {
			return upload, fmt.Errorf("close multipart field: %w", err)
		}
	}
	if upload.Path == "" {
		return upload, errors.New("import file is required")
	}
	if upload.Source != "plausible" && upload.Source != "umami" {
		return upload, errors.New("source must be plausible or umami")
	}
	return upload, nil
}

func (h *handler) createImport(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	if h.deps.Imports == nil || h.deps.ImportQueue == nil {
		unavailable(w, "import")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes+(1<<20))
	upload, err := saveImportMultipart(r, "", maxImportBytes)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errImportTooLarge) || strings.Contains(err.Error(), "request body too large") {
			status = http.StatusRequestEntityTooLarge
		}
		writeProblem(w, status, http.StatusText(status), err.Error())
		return
	}
	replaceValue := r.URL.Query().Get("replace")
	if replaceValue != "" && replaceValue != "true" && replaceValue != "false" {
		_ = os.Remove(upload.Path)
		writeProblem(w, http.StatusBadRequest, "Bad request", "replace must be true or false")
		return
	}
	created, err := h.deps.Imports.Create(r.Context(), imports.CreateInput{
		SiteID: site.ID, Source: imports.Source(upload.Source), FileName: upload.FileName, CreatedBy: principal.UserID,
		FileHash: upload.FileHash, Replace: replaceValue == "true",
	})
	if err != nil {
		_ = os.Remove(upload.Path)
		var duplicate *imports.AlreadyImportedError
		if errors.As(err, &duplicate) {
			writeProblem(w, http.StatusConflict, "Conflict", "this file was already imported on "+duplicate.ImportedAt.UTC().Format(time.DateOnly))
			return
		}
		serviceError(w, err)
		return
	}
	if err := h.deps.ImportQueue.Queue(imports.Job{ImportID: created.ID, Source: created.Source, Path: upload.Path}); err != nil {
		_ = os.Remove(upload.Path)
		_ = h.deps.Imports.SetFailed(context.Background(), created.ID, err)
		writeProblem(w, http.StatusServiceUnavailable, "Service unavailable", "the import worker is busy")
		return
	}
	if err := h.auditMutation(r.Context(), principal, "import.created", "import", created.ID, site.ID); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, created)
}

func (h *handler) listImports(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	if h.deps.Imports == nil {
		unavailable(w, "import")
		return
	}
	items, err := h.deps.Imports.List(r.Context(), site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *handler) deleteImport(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}
	if h.deps.Imports == nil {
		unavailable(w, "import")
		return
	}
	id, err := parseInt64(r.PathValue("import"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "import not found")
		return
	}
	item, err := h.deps.Imports.Get(r.Context(), id)
	if errors.Is(err, imports.ErrNotFound) {
		writeProblem(w, http.StatusNotFound, "Not found", "import not found")
		return
	}
	if err != nil {
		serviceError(w, err)
		return
	}
	principal, _, ok = h.visibleSite(w, principal, item.SiteID)
	if !ok {
		return
	}
	if !roleAllowed(principal.Role, []authz.Role{authz.RoleOwner, authz.RoleAdmin}) || !authz.Can(principal, authz.ActionSiteEdit, item.SiteID) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
		return
	}
	deleted, err := h.deps.Imports.Delete(r.Context(), id)
	if err != nil {
		serviceError(w, err)
		return
	}
	if !deleted {
		writeProblem(w, http.StatusNotFound, "Not found", "import not found")
		return
	}
	if err := h.auditMutation(r.Context(), principal, "import.deleted", "import", id, item.SiteID); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) exportSite(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	if h.deps.Exporter == nil {
		unavailable(w, "export")
		return
	}
	from, to, err := exportRange(r)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
		return
	}
	if err := h.auditMutation(r.Context(), principal, "site.exported", "site", int64(site.ID), site.ID); err != nil {
		serviceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="panel-site-%d-%s-%s.zip"`, site.ID, from.Format(time.DateOnly), to.Format(time.DateOnly)))
	if err := h.deps.Exporter.Write(r.Context(), w, site.ID, from, to); err != nil {
		// A streaming response may already have started; the server log will retain
		// the error while the incomplete ZIP makes the failure visible to clients.
		return
	}
}

func exportRange(r *http.Request) (time.Time, time.Time, error) {
	from, err := time.Parse(time.DateOnly, r.URL.Query().Get("from"))
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("from must be an ISO date")
	}
	to, err := time.Parse(time.DateOnly, r.URL.Query().Get("to"))
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("to must be an ISO date")
	}
	if from.After(to) {
		return time.Time{}, time.Time{}, errors.New("from must not be after to")
	}
	return from, to, nil
}

func (h *handler) auditMutation(ctx context.Context, principal authz.Principal, action, targetType string, targetID int64, siteID int32) error {
	if h.deps.Store == nil {
		return nil
	}
	return h.deps.Store.Tx(ctx, func(tx pgx.Tx) error {
		return audit.Write(ctx, tx, principal, action, targetType, targetID, map[string]any{"site_id": siteID})
	})
}
