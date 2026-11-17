package sentryapi

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // the upload protocol identifies bundles by SHA-1
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/sourcemap"
)

type assembleRequest struct {
	Checksum string   `json:"checksum"`
	Chunks   []string `json:"chunks"`
	Projects []string `json:"projects"`
	Version  string   `json:"version"`
	Dist     string   `json:"dist"`
}

type assembleResponse struct {
	State         string   `json:"state"`
	MissingChunks []string `json:"missingChunks"`
	Detail        *string  `json:"detail"`
}

// assemble reports which chunks of a bundle are missing, or queues assembly
// once all are present. A repeated call returns the bundle's current state.
func (h *Handler) assemble(w http.ResponseWriter, r *http.Request, c *call) {
	var req assembleRequest
	if !readJSON(w, r, &req) {
		return
	}
	req.Checksum = strings.ToLower(req.Checksum)
	if !validSHA1(req.Checksum) || len(req.Chunks) == 0 || len(req.Chunks) > maxChunksPerFile || len(req.Version) > 200 || len(req.Dist) > 64 {
		writeDetail(w, http.StatusBadRequest, "invalid assemble request")
		return
	}
	for i, chunk := range req.Chunks {
		req.Chunks[i] = strings.ToLower(chunk)
		if !validSHA1(req.Chunks[i]) {
			writeDetail(w, http.StatusBadRequest, "invalid chunk checksum")
			return
		}
	}
	siteIDs, perr := h.resolveProjects(c, req.Projects)
	if perr != nil {
		writeDetail(w, perr.status, perr.detail)
		return
	}
	ctx := r.Context()
	state, detail, bundleSites, found, err := h.bundleState(ctx, c.orgID, req.Checksum)
	if err != nil {
		serverError(w)
		return
	}
	if found {
		if !c.canUseSites(bundleSites) {
			notFound(w)
			return
		}
		writeJSON(w, http.StatusOK, assembleResponse{State: state, MissingChunks: []string{}, Detail: detail})
		return
	}
	missing := []string{}
	for _, chunk := range req.Chunks {
		ok, err := h.chunkExists(r, chunk)
		if err != nil {
			serverError(w)
			return
		}
		if !ok && !contains(missing, chunk) {
			missing = append(missing, chunk)
		}
	}
	if len(missing) > 0 {
		writeJSON(w, http.StatusOK, assembleResponse{State: "not_found", MissingChunks: missing})
		return
	}
	if siteIDs == nil {
		siteIDs = []int32{}
	}
	var id int64
	err = h.deps.Pool.QueryRow(ctx, `
		insert into app_artifact_bundles(org_id, site_ids, release, dist, checksum, chunks)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (org_id, checksum) do nothing
		returning id`, c.orgID, siteIDs, req.Version, req.Dist, req.Checksum, req.Chunks).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) { // lost a race with an identical request
		var bundleSites []int32
		state, detail, bundleSites, _, err = h.bundleState(ctx, c.orgID, req.Checksum)
		if err != nil {
			serverError(w)
			return
		}
		if !c.canUseSites(bundleSites) {
			notFound(w)
			return
		}
		writeJSON(w, http.StatusOK, assembleResponse{State: state, MissingChunks: []string{}, Detail: detail})
		return
	}
	if err != nil {
		serverError(w)
		return
	}
	select {
	case h.queue <- id:
		writeJSON(w, http.StatusOK, assembleResponse{State: "created", MissingChunks: []string{}})
	default:
		_, _ = h.deps.Pool.Exec(ctx, `delete from app_artifact_bundles where id=$1`, id)
		w.Header().Set("Retry-After", "30")
		writeDetail(w, http.StatusServiceUnavailable, "assembly queue is full, retry shortly")
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func (h *Handler) bundleState(ctx context.Context, orgID int64, checksum string) (state string, detail *string, siteIDs []int32, found bool, err error) {
	err = h.deps.Pool.QueryRow(ctx, `select state, detail, site_ids from app_artifact_bundles where org_id=$1 and checksum=$2`, orgID, checksum).Scan(&state, &detail, &siteIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, nil, false, nil
	}
	if err != nil {
		return "", nil, nil, false, err
	}
	if state == "created" || state == "assembling" {
		detail = nil
	}
	return state, detail, siteIDs, true, nil
}

func (h *Handler) fail(ctx context.Context, id int64, detail string) {
	if len(detail) > 300 {
		detail = detail[:300]
	}
	if _, err := h.deps.Pool.Exec(ctx, `update app_artifact_bundles set state='error', detail=$2 where id=$1`, id, detail); err != nil {
		slog.Error("sentryapi: record assemble failure", "bundle", id, "err", err)
	}
}

// assembleBundle concatenates the chunks into a temp file, verifies the
// checksum, parses the bundle within its caps and stores each file as its own
// blob so a lookup reads one object.
func (h *Handler) assembleBundle(ctx context.Context, id int64) {
	var orgID int64
	var chunks []string
	var checksum, release, dist string
	var siteIDs []int32
	err := h.deps.Pool.QueryRow(ctx, `update app_artifact_bundles set state='assembling' where id=$1 returning org_id, checksum, release, dist, site_ids, chunks`, id).Scan(&orgID, &checksum, &release, &dist, &siteIDs, &chunks)
	if err != nil {
		slog.Error("sentryapi: load bundle", "bundle", id, "err", err)
		return
	}
	if h.deps.TempDir != "" {
		_ = os.MkdirAll(h.deps.TempDir, 0o700)
	}
	tmp, err := os.CreateTemp(h.deps.TempDir, "bundle-*.zip")
	if err != nil {
		h.fail(ctx, id, "storage unavailable")
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	hash := sha1.New() //nolint:gosec // see import
	var size int64
	for _, chunk := range chunks {
		rc, err := h.deps.Blobs.Open(ctx, chunkKey(chunk))
		if err != nil {
			h.fail(ctx, id, "a chunk is missing, upload the bundle again")
			return
		}
		n, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(rc, chunkSize+1))
		_ = rc.Close()
		size += n
		if err != nil || n > chunkSize || size > h.deps.Caps.MaxTotal+chunkSize {
			h.fail(ctx, id, "bundle exceeds the size limit")
			return
		}
	}
	if hex.EncodeToString(hash.Sum(nil)) != checksum {
		h.fail(ctx, id, "chunks do not match the bundle checksum")
		return
	}
	var used int64
	if err := h.deps.Pool.QueryRow(ctx, `select coalesce(sum(size),0) from app_artifact_bundles where org_id=$1 and state='ok'`, orgID).Scan(&used); err != nil {
		h.fail(ctx, id, "storage unavailable")
		return
	}
	if used+size > h.deps.OrgCapBytes {
		h.fail(ctx, id, fmt.Sprintf("organization source map storage limit reached: %d of %d bytes used, this bundle needs %d more; delete old releases or raise SUPERMETRY_SENTRY_ORG_CAP", used, h.deps.OrgCapBytes, size))
		return
	}
	bundle, err := sourcemap.ParseBundle(tmp, size, h.deps.Caps)
	if err != nil {
		h.fail(ctx, id, "invalid artifact bundle: "+err.Error())
		return
	}
	if release == "" {
		release = bundle.Release
	}
	if dist == "" {
		dist = bundle.Dist
	}
	prefix := fmt.Sprintf("bundles/%d/%s/", orgID, checksum)
	type row struct {
		file sourcemap.BundleFile
		key  string
	}
	var rows []row
	for _, f := range bundle.Files {
		clean := path.Clean("/" + f.Path)[1:]
		if clean == "" || strings.Contains(clean, "..") {
			continue
		}
		data, err := bundle.ReadFile(f.Path)
		if err != nil {
			h.fail(ctx, id, "invalid artifact bundle: "+err.Error())
			return
		}
		key := prefix + clean
		if _, err := h.deps.Blobs.PutIfAbsent(ctx, key, bytes.NewReader(data)); err != nil {
			h.fail(ctx, id, "storage unavailable")
			return
		}
		rows = append(rows, row{f, key})
	}
	tx, err := h.deps.Pool.Begin(ctx)
	if err != nil {
		h.fail(ctx, id, "storage unavailable")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	targets := []*int32{nil}
	if len(siteIDs) > 0 {
		targets = targets[:0]
		for i := range siteIDs {
			targets = append(targets, &siteIDs[i])
		}
	}
	batch := &pgx.Batch{}
	for _, r := range rows {
		var debugID *string
		if d := sourcemap.NormalizeDebugID(r.file.DebugID); d != "" {
			debugID = &d
		}
		for _, site := range targets {
			batch.Queue(`insert into app_artifact_files(bundle_id, org_id, site_id, debug_id, release, dist, url, type, blob_key, sourcemap_ref, size)
				values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, id, orgID, site, debugID, release, dist, r.file.URL, r.file.Type, r.key, r.file.SourceMapRef, int64(r.file.Size))
		}
	}
	if err := sendBatch(ctx, tx, batch); err != nil {
		h.fail(ctx, id, "storage unavailable")
		return
	}
	if _, err := tx.Exec(ctx, `update app_artifact_bundles set state='ok', detail=null, size=$2, release=$3, dist=$4 where id=$1`, id, size, release, dist); err != nil {
		h.fail(ctx, id, "storage unavailable")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.fail(ctx, id, "storage unavailable")
	}
}

func sendBatch(ctx context.Context, tx pgx.Tx, batch *pgx.Batch) error {
	if batch.Len() == 0 {
		return nil
	}
	br := tx.SendBatch(ctx, batch)
	for i := 0; i < batch.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return err
		}
	}
	return br.Close()
}
