package platformhttp

import (
	"context"
	"net/http"
	"os"
	"time"

	"supermetry/internal/imports"
	"supermetry/internal/platform/authz"
)

// importPreviewTimeout bounds one synchronous dry-run parse.
const importPreviewTimeout = 2 * time.Minute

// importPreviewSlots bounds concurrent dry runs per process: each one parses a
// whole upload into memory-bounded aggregates on the request goroutine.
var importPreviewSlots = make(chan struct{}, 2)

// importPreviewer is implemented by the import store; a dry run reads stored
// data to report overlap but never writes.
type importPreviewer interface {
	Preview(context.Context, int32, [32]byte, imports.Aggregate) (imports.Overlap, error)
}

type importPreview struct {
	Source   string `json:"source"`
	FileName string `json:"file_name"`
	imports.Summary
	Overlap imports.Overlap `json:"overlap"`
}

// previewImport parses an upload and reports what confirming it would import:
// counts, date range, row errors and overlap with stored data. Nothing is
// stored and the temporary file is removed before the response is written.
func (h *handler) previewImport(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	if !h.allowHistoricalIdentity(w) {
		return
	}
	previewer, available := h.deps.Imports.(importPreviewer)
	if h.deps.Imports == nil || !available {
		unavailable(w, "import preview")
		return
	}
	select {
	case importPreviewSlots <- struct{}{}:
		defer func() { <-importPreviewSlots }()
	default:
		w.Header().Set("Retry-After", "5")
		writeProblem(w, http.StatusTooManyRequests, "Too many requests", "another import preview is already running")
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(importUploadTimeout))
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes+(1<<20))
	upload, err := saveImportMultipart(r, "", maxImportBytes)
	if err != nil {
		status := http.StatusBadRequest
		if err == errImportTooLarge {
			status = http.StatusRequestEntityTooLarge
		}
		writeProblem(w, status, http.StatusText(status), err.Error())
		return
	}
	defer os.Remove(upload.Path)
	ctx, cancel := context.WithTimeout(r.Context(), importPreviewTimeout)
	defer cancel()
	location := site.TZ
	if location == nil {
		location = time.UTC
	}
	aggregate, stats, err := imports.ProcessFileInLocation(ctx, imports.Source(upload.Source), upload.Path, location)
	if err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, "Unprocessable import file", err.Error())
		return
	}
	overlap, err := previewer.Preview(ctx, site.ID, upload.FileHash, aggregate)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, importPreview{
		Source: upload.Source, FileName: upload.FileName, Summary: imports.Summarize(aggregate, stats), Overlap: overlap,
	})
}
