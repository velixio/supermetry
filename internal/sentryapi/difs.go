package sentryapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/debugfile"
	"supermetry/internal/platform/authz"
)

const (
	// maxDIFRequestFiles bounds one difs/assemble request.
	maxDIFRequestFiles = 1000
	// maxDIFRequestChunks bounds the chunk lookups one request may cause.
	maxDIFRequestChunks = 4096
	// defaultDIFMaxBytes is the per debug file size cap (SUPERMETRY_DIF_MAX_MB).
	defaultDIFMaxBytes = 512 << 20
	difQueueSize       = 64
	// maxRequeue is how many interrupted or outdated uploads one start re-queues.
	maxRequeue = 64
	maxNameLen = 1024
)

// CacheBuilder converts a raw debug object into the compact cache a resolver
// reads. Version is stored with the cache; raising it rebuilds existing caches
// from the kept raw files on the next start. Build must treat raw as hostile:
// it is already size-capped, but its headers are not trusted.
type CacheBuilder struct {
	Version int16
	Build   func(ctx context.Context, obj debugfile.Object, raw io.ReaderAt, size int64, out io.Writer) (features []string, err error)
}

type difEntry struct {
	Name    string   `json:"name"`
	DebugID string   `json:"debug_id"`
	Chunks  []string `json:"chunks"`
}

type difData struct {
	Features []string `json:"features"`
}

type difInfo struct {
	ID          string    `json:"id"`
	UUID        string    `json:"uuid"`
	DebugID     string    `json:"debugId"`
	CodeID      string    `json:"codeId,omitempty"`
	ObjectName  string    `json:"objectName"`
	CPUName     string    `json:"cpuName"`
	SymbolType  string    `json:"symbolType"`
	SHA1        string    `json:"sha1"`
	Size        int64     `json:"size"`
	DateCreated time.Time `json:"dateCreated"`
	Data        difData   `json:"data"`
}

type difResponse struct {
	State         string   `json:"state"`
	MissingChunks []string `json:"missingChunks"`
	Detail        *string  `json:"detail"`
	Dif           *difInfo `json:"dif,omitempty"`
}

// difsAssemble answers sentry-cli's debug file upload poll. Per file it
// reports the missing chunks, or queues assembly once all are present; a
// repeated call returns the current state, which stays assembling or created
// (never not_found) while the file is being converted.
func (h *Handler) difsAssemble(w http.ResponseWriter, r *http.Request, c *call) {
	site, ok := h.findSite(c.orgID, r.PathValue("project"))
	if !ok {
		writeDetail(w, http.StatusNotFound, "project not found")
		return
	}
	if !authz.Can(c.principal, authz.ActionReleasesWrite, site.ID) {
		writeDetail(w, http.StatusForbidden, "the token may not use this project")
		return
	}
	var req map[string]difEntry
	if !readJSON(w, r, &req) {
		return
	}
	if len(req) == 0 || len(req) > maxDIFRequestFiles {
		writeDetail(w, http.StatusBadRequest, "invalid assemble request")
		return
	}
	total := 0
	for sum, e := range req {
		total += len(e.Chunks)
		if !validSHA1(sum) || len(e.Chunks) == 0 || len(e.Chunks) > maxChunksPerFile || total > maxDIFRequestChunks {
			writeDetail(w, http.StatusBadRequest, "invalid assemble request")
			return
		}
		for _, chunk := range e.Chunks {
			if !validSHA1(chunk) {
				writeDetail(w, http.StatusBadRequest, "invalid chunk checksum")
				return
			}
		}
	}
	out := make(map[string]difResponse, len(req))
	for sum, e := range req {
		resp, status := h.assembleDIF(r, c, site.ID, sum, e)
		if status != 0 {
			switch status {
			case http.StatusNotFound:
				notFound(w)
			case http.StatusServiceUnavailable:
				w.Header().Set("Retry-After", "30")
				writeDetail(w, status, "assembly queue is full, retry shortly")
			default:
				serverError(w)
			}
			return
		}
		out[sum] = resp
	}
	writeJSON(w, http.StatusOK, out)
}

// assembleDIF handles one file of the request. A non-zero status aborts the
// whole request with that HTTP status.
func (h *Handler) assembleDIF(r *http.Request, c *call, siteID int32, sum string, e difEntry) (difResponse, int) {
	ctx := r.Context()
	state, detail, found, status := h.difState(ctx, c, siteID, sum)
	if status != 0 {
		return difResponse{}, status
	}
	if found {
		return h.difReply(ctx, c.orgID, siteID, sum, state, detail), 0
	}
	var missing []string
	for _, chunk := range e.Chunks {
		ok, err := h.chunkExists(r, chunk)
		if err != nil {
			return difResponse{}, http.StatusInternalServerError
		}
		if !ok && !slices.Contains(missing, chunk) {
			missing = append(missing, chunk)
		}
	}
	if len(missing) > 0 {
		return difResponse{State: "not_found", MissingChunks: missing}, 0
	}
	declared := debugfile.NormalizeDebugID(e.DebugID)
	var id int64
	err := h.deps.Pool.QueryRow(ctx, `
		insert into app_dif_uploads(org_id, site_ids, checksum, name, declared_debug_id, chunks)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (org_id, checksum) do nothing
		returning id`, c.orgID, []int32{siteID}, sum, clipString(e.Name, maxNameLen), nullable(declared), e.Chunks).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) { // lost a race with an identical request
		state, detail, found, status = h.difState(ctx, c, siteID, sum)
		if status != 0 || !found {
			return difResponse{}, max(status, http.StatusInternalServerError)
		}
		return h.difReply(ctx, c.orgID, siteID, sum, state, detail), 0
	}
	if err != nil {
		return difResponse{}, http.StatusInternalServerError
	}
	select {
	case h.difQueue <- id:
		return difResponse{State: "created", MissingChunks: []string{}}, 0
	default:
		_, _ = h.deps.Pool.Exec(ctx, `delete from app_dif_uploads where id=$1`, id)
		return difResponse{}, http.StatusServiceUnavailable
	}
}

// difState loads an existing upload and makes sure it covers the project: an
// upload bound to other projects of the organization is extended to this one.
// A 404 status means the key may not see the upload at all.
func (h *Handler) difState(ctx context.Context, c *call, siteID int32, sum string) (state string, detail *string, found bool, status int) {
	tx, err := h.deps.Pool.Begin(ctx)
	if err != nil {
		return "", nil, false, http.StatusInternalServerError
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var id int64
	var sites []int32
	err = tx.QueryRow(ctx, `select id, state, detail, site_ids from app_dif_uploads where org_id=$1 and checksum=$2 for update`, c.orgID, sum).Scan(&id, &state, &detail, &sites)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, false, 0
	}
	if err != nil {
		return "", nil, false, http.StatusInternalServerError
	}
	if !c.canUseSites(sites) {
		return "", nil, false, http.StatusNotFound
	}
	if len(sites) > 0 && !slices.Contains(sites, siteID) {
		if _, err := tx.Exec(ctx, `update app_dif_uploads set site_ids = array_append(site_ids, $2) where id=$1`, id, siteID); err != nil {
			return "", nil, false, http.StatusInternalServerError
		}
		// Rows already written for the upload get a sibling for the new project.
		if _, err := tx.Exec(ctx, `
			insert into app_debug_files(upload_id, org_id, site_id, debug_id, code_id, kind, arch, object_name, features, cache_state, cache_version, cache_key, cache_size, detail)
			select upload_id, org_id, $3, debug_id, code_id, kind, arch, object_name, features, cache_state, cache_version, cache_key, cache_size, detail
			from app_debug_files where upload_id=$1 and site_id=$2`, id, sites[0], siteID); err != nil {
			return "", nil, false, http.StatusInternalServerError
		}
	}
	if state == "created" || state == "assembling" {
		detail = nil
	}
	if err := tx.Commit(ctx); err != nil {
		return "", nil, false, http.StatusInternalServerError
	}
	return state, detail, true, 0
}

func (h *Handler) difReply(ctx context.Context, orgID int64, siteID int32, sum, state string, detail *string) difResponse {
	resp := difResponse{State: state, MissingChunks: []string{}, Detail: detail}
	if state != "ok" {
		return resp
	}
	var info difInfo
	err := h.deps.Pool.QueryRow(ctx, `
		select f.id::text, f.debug_id, f.code_id, f.object_name, f.arch, f.kind, f.features, u.size, u.created_at
		from app_debug_files f join app_dif_uploads u on u.id = f.upload_id
		where u.org_id=$1 and u.checksum=$2 and (f.site_id=$3 or f.site_id is null)
		order by f.id limit 1`, orgID, sum, siteID).Scan(&info.ID, &info.DebugID, &info.CodeID, &info.ObjectName, &info.CPUName, &info.SymbolType, &info.Data.Features, &info.Size, &info.DateCreated)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Error("sentryapi: load debug file", "err", err)
		}
		return resp
	}
	if info.Data.Features == nil {
		info.Data.Features = []string{}
	}
	info.UUID, info.SHA1 = info.DebugID, sum
	resp.Dif = &info
	return resp
}

func clipString(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	return strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "")
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ---------------------------------------------------------------- worker

func (h *Handler) failDIF(ctx context.Context, id int64, detail string) {
	detail = clipString(detail, 300)
	if _, err := h.deps.Pool.Exec(ctx, `update app_dif_uploads set state='error', detail=$2 where id=$1`, id, detail); err != nil {
		slog.Error("sentryapi: record debug file failure", "upload", id, "err", err)
	}
}

func (h *Handler) difMax() int64 {
	if h.deps.DIFMaxBytes > 0 {
		return h.deps.DIFMaxBytes
	}
	return defaultDIFMaxBytes
}

type difUpload struct {
	id       int64
	orgID    int64
	checksum string
	name     string
	declared *string
	chunks   []string
	state    string
	blobKey  *string
	size     int64
}

// processDIF drives one upload to a final state: concatenate and verify the
// chunks, detect the objects, keep the raw file, then convert each object to
// its cache. The upload stays created/assembling until the caches are built.
// It is idempotent, so a restart simply runs it again.
func (h *Handler) processDIF(ctx context.Context, id int64) {
	var u difUpload
	err := h.deps.Pool.QueryRow(ctx, `select id, org_id, checksum, name, declared_debug_id, chunks, state, blob_key, size from app_dif_uploads where id=$1`, id).
		Scan(&u.id, &u.orgID, &u.checksum, &u.name, &u.declared, &u.chunks, &u.state, &u.blobKey, &u.size)
	if err != nil {
		slog.Error("sentryapi: load debug file upload", "upload", id, "err", err)
		return
	}
	switch u.state {
	case "created", "assembling":
		h.assembleDIFUpload(ctx, u)
	case "ok":
		h.rebuildDIF(ctx, u)
	}
}

func (h *Handler) assembleDIFUpload(ctx context.Context, u difUpload) {
	if _, err := h.deps.Pool.Exec(ctx, `update app_dif_uploads set state='assembling' where id=$1`, u.id); err != nil {
		return
	}
	max := h.difMax()
	tmp, size, sum, err := h.concatChunks(ctx, "dif-*.bin", u.chunks, max)
	switch {
	case errors.Is(err, errChunkMissing):
		h.failDIF(ctx, u.id, "a chunk is missing, upload the debug file again")
		return
	case errors.Is(err, errTooLarge):
		h.failDIF(ctx, u.id, "debug file exceeds the size limit")
		return
	case err != nil:
		if ctx.Err() == nil {
			h.failDIF(ctx, u.id, "storage unavailable")
		}
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if sum != u.checksum {
		h.failDIF(ctx, u.id, "chunks do not match the debug file checksum")
		return
	}
	var used int64
	if err := h.deps.Pool.QueryRow(ctx, `select (select coalesce(sum(size),0) from app_artifact_bundles where org_id=$1 and state='ok') + (select coalesce(sum(size),0) from app_dif_uploads where org_id=$1 and state='ok')`, u.orgID).Scan(&used); err != nil {
		h.failDIF(ctx, u.id, "storage unavailable")
		return
	}
	if used+size > h.deps.OrgCapBytes {
		h.failDIF(ctx, u.id, fmt.Sprintf("organization debug file and source map storage limit reached: %d of %d bytes used, this file needs %d more; delete old releases or raise SUPERMETRY_SENTRY_ORG_CAP", used, h.deps.OrgCapBytes, size))
		return
	}
	objs, err := debugfile.Detect(tmp, size, u.name)
	if err != nil {
		h.failDIF(ctx, u.id, "invalid debug file: "+err.Error())
		return
	}
	for i := range objs {
		if objs[i].DebugID == "" && u.declared != nil {
			objs[i].DebugID = *u.declared
		}
		if objs[i].DebugID == "" && objs[i].Kind == "proguard" {
			h.failDIF(ctx, u.id, "invalid debug file: a ProGuard mapping needs a UUID in its name or a debug_id")
			return
		}
	}
	blobKey := fmt.Sprintf("difs/%d/%s", u.orgID, u.checksum)
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		h.failDIF(ctx, u.id, "storage unavailable")
		return
	}
	if _, err := h.deps.Blobs.PutIfAbsent(ctx, blobKey, tmp); err != nil {
		h.failDIF(ctx, u.id, "storage unavailable")
		return
	}
	if err := h.insertObjects(ctx, u.id, u.orgID, blobKey, size, objs); err != nil {
		slog.Error("sentryapi: record debug objects", "upload", u.id, "err", err)
		h.failDIF(ctx, u.id, "storage unavailable")
		return
	}
	built, failed, lastErr := h.buildCaches(ctx, u.orgID, u.id, u.checksum, tmp, size, objs, false)
	if ctx.Err() != nil {
		return // shut down mid-build: the upload stays assembling and is re-queued on start
	}
	if built == 0 && failed > 0 {
		h.failDIF(ctx, u.id, "could not read the debug file: "+lastErr)
		return
	}
	if _, err := h.deps.Pool.Exec(ctx, `update app_dif_uploads set state='ok', detail=null where id=$1`, u.id); err != nil {
		slog.Error("sentryapi: finish debug file", "upload", u.id, "err", err)
	}
}

// insertObjects replaces the upload's object rows, one per object and target
// project, and records the raw blob.
func (h *Handler) insertObjects(ctx context.Context, id, orgID int64, blobKey string, size int64, objs []debugfile.Object) error {
	tx, err := h.deps.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var siteIDs []int32
	if err := tx.QueryRow(ctx, `select site_ids from app_dif_uploads where id=$1 for update`, id).Scan(&siteIDs); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `delete from app_debug_files where upload_id=$1`, id); err != nil {
		return err
	}
	targets := []*int32{nil}
	if len(siteIDs) > 0 {
		targets = targets[:0]
		for i := range siteIDs {
			targets = append(targets, &siteIDs[i])
		}
	}
	batch := &pgx.Batch{}
	for _, o := range objs {
		features := o.Features
		if features == nil {
			features = []string{}
		}
		for _, site := range targets {
			batch.Queue(`insert into app_debug_files(upload_id, org_id, site_id, debug_id, code_id, kind, arch, object_name, features)
				values ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, orgID, site, o.DebugID, o.CodeID, o.Kind, o.Arch, o.Name, features)
		}
	}
	if err := sendBatch(ctx, tx, batch); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update app_dif_uploads set blob_key=$2, size=$3 where id=$1`, id, blobKey, size); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// buildCaches runs the registered builder of each object's kind. Objects whose
// kind has no builder stay pending (or, for kinds nothing converts, become
// unsupported). With onlyStale set, objects whose cache is current are skipped.
func (h *Handler) buildCaches(ctx context.Context, orgID, uploadID int64, checksum string, raw io.ReaderAt, size int64, objs []debugfile.Object, onlyStale bool) (built, failed int, lastErr string) {
	for _, o := range objs {
		if ctx.Err() != nil {
			return
		}
		b, ok := h.deps.Builders[o.Kind]
		if !ok || b.Build == nil {
			continue
		}
		if onlyStale {
			var stale bool
			err := h.deps.Pool.QueryRow(ctx, `select exists(select 1 from app_debug_files where upload_id=$1 and debug_id=$2 and kind=$3 and arch=$4 and (cache_state='pending' or (cache_state='ok' and cache_version < $5)))`,
				uploadID, o.DebugID, o.Kind, o.Arch, b.Version).Scan(&stale)
			if err != nil || !stale {
				continue
			}
		}
		key, csize, features, err := h.buildOne(ctx, orgID, checksum, b, o, raw, size)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			failed++
			lastErr = clipString(err.Error(), 200)
			_, _ = h.deps.Pool.Exec(ctx, `update app_debug_files set cache_state='error', detail=$5 where upload_id=$1 and debug_id=$2 and kind=$3 and arch=$4`, uploadID, o.DebugID, o.Kind, o.Arch, lastErr)
			continue
		}
		built++
		h.dropOldCaches(ctx, uploadID, o, key)
		if len(features) == 0 {
			features = o.Features
		}
		if features == nil {
			features = []string{}
		}
		if _, err := h.deps.Pool.Exec(ctx, `update app_debug_files set cache_state='ok', cache_version=$5, cache_key=$6, cache_size=$7, features=$8, detail=null
			where upload_id=$1 and debug_id=$2 and kind=$3 and arch=$4`, uploadID, o.DebugID, o.Kind, o.Arch, b.Version, key, csize, features); err != nil {
			slog.Error("sentryapi: record cache", "upload", uploadID, "err", err)
			continue
		}
		h.clearMissing(ctx, orgID, uploadID, o)
	}
	return
}

// clearMissing removes the missing-symbols rows a freshly usable object
// covers: same kind and debug id, in the projects the upload targets.
func (h *Handler) clearMissing(ctx context.Context, orgID, uploadID int64, o debugfile.Object) {
	if _, err := h.deps.Pool.Exec(ctx, `
		delete from app_missing_debug_files m using sites s
		where s.id = m.site_id and s.org_id = $1 and m.kind = $2 and m.debug_id = $3
		  and exists (select 1 from app_debug_files f where f.upload_id = $4 and f.kind = m.kind and f.debug_id = m.debug_id
		              and (f.site_id is null or f.site_id = m.site_id))`, orgID, o.Kind, o.DebugID, uploadID); err != nil {
		slog.Error("sentryapi: clear missing debug files", "upload", uploadID, "err", err)
	}
}

// dropOldCaches deletes the cache blobs an object had before a rebuild, once
// no object row refers to them. It runs before the rows are repointed, so the
// object's own rows are excluded from the check.
func (h *Handler) dropOldCaches(ctx context.Context, uploadID int64, o debugfile.Object, newKey string) {
	rows, err := h.deps.Pool.Query(ctx, `select distinct cache_key from app_debug_files where upload_id=$1 and debug_id=$2 and kind=$3 and arch=$4 and cache_key is not null and cache_key<>$5`, uploadID, o.DebugID, o.Kind, o.Arch, newKey)
	if err != nil {
		return
	}
	var old []string
	for rows.Next() {
		var k string
		if rows.Scan(&k) == nil {
			old = append(old, k)
		}
	}
	rows.Close()
	for _, k := range old {
		var other bool
		if err := h.deps.Pool.QueryRow(ctx, `select exists(select 1 from app_debug_files where cache_key=$1 and not (upload_id=$2 and debug_id=$3 and kind=$4 and arch=$5))`, k, uploadID, o.DebugID, o.Kind, o.Arch).Scan(&other); err == nil && !other {
			_, _ = h.deps.Blobs.Delete(ctx, k)
		}
	}
}

func (h *Handler) buildOne(ctx context.Context, orgID int64, checksum string, b CacheBuilder, o debugfile.Object, raw io.ReaderAt, size int64) (key string, csize int64, features []string, err error) {
	out, err := os.CreateTemp(h.deps.TempDir, "difcache-*.bin")
	if err != nil {
		return "", 0, nil, err
	}
	defer os.Remove(out.Name())
	defer out.Close()
	features, err = func() (f []string, err error) {
		defer func() {
			if r := recover(); r != nil { // a parser bug on hostile input must not take the process down
				err = fmt.Errorf("cache builder panic: %v", r)
			}
		}()
		return b.Build(ctx, o, raw, size, &limitedWriter{w: out, left: h.difMax()})
	}()
	if err != nil {
		return "", 0, nil, err
	}
	csize, err = out.Seek(0, io.SeekCurrent)
	if err != nil {
		return "", 0, nil, err
	}
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		return "", 0, nil, err
	}
	// The upload checksum is part of the key: a different file with the same
	// debug id gets its own cache and never reuses (or poisons) an earlier one.
	key = fmt.Sprintf("difcache/%d/%s/%s/%s-v%d", orgID, checksum, o.DebugID, o.Kind, b.Version)
	if o.Slice > 0 {
		key += "-s" + strconv.Itoa(o.Slice)
	}
	if _, err := h.deps.Blobs.PutIfAbsent(ctx, key, out); err != nil {
		return "", 0, nil, err
	}
	return key, csize, features, nil
}

var errCacheTooLarge = errors.New("cache exceeds the size limit")

// limitedWriter fails once more than left bytes are written.
type limitedWriter struct {
	w    io.Writer
	left int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.left {
		return 0, errCacheTooLarge
	}
	l.left -= int64(len(p))
	return l.w.Write(p)
}

// rebuildDIF converts objects of a finished upload whose cache is pending or
// older than its builder, reading the kept raw file again.
func (h *Handler) rebuildDIF(ctx context.Context, u difUpload) {
	if u.blobKey == nil {
		return
	}
	if h.deps.TempDir != "" {
		_ = os.MkdirAll(h.deps.TempDir, 0o700)
	}
	tmp, err := os.CreateTemp(h.deps.TempDir, "dif-*.bin")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	rc, err := h.deps.Blobs.Open(ctx, *u.blobKey)
	if err != nil {
		slog.Warn("sentryapi: raw debug file missing", "upload", u.id, "err", err)
		return
	}
	n, err := io.Copy(tmp, io.LimitReader(rc, h.difMax()+1))
	_ = rc.Close()
	if err != nil || n > h.difMax() {
		return
	}
	objs, err := debugfile.Detect(tmp, n, u.name)
	if err != nil {
		return
	}
	for i := range objs {
		if objs[i].DebugID == "" && u.declared != nil {
			objs[i].DebugID = *u.declared
		}
	}
	h.buildCaches(ctx, u.orgID, u.id, u.checksum, tmp, n, objs, true)
}

// requeueDIFs puts uploads a restart interrupted, and finished ones whose
// caches are pending or outdated, back on the queue.
func (h *Handler) requeueDIFs(ctx context.Context) {
	ids := map[int64]struct{}{}
	collect := func(q string, args ...any) {
		rows, err := h.deps.Pool.Query(ctx, q, args...)
		if err != nil {
			slog.Error("sentryapi: recover debug files", "err", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil && len(ids) < maxRequeue {
				ids[id] = struct{}{}
			}
		}
	}
	collect(`select id from app_dif_uploads where state in ('created','assembling') order by id limit $1`, maxRequeue)
	for kind, b := range h.deps.Builders {
		collect(`select distinct upload_id from app_debug_files where kind=$1 and (cache_state='pending' or (cache_state='ok' and cache_version < $2)) limit $3`, kind, b.Version, maxRequeue)
	}
	for id := range ids {
		select {
		case h.difQueue <- id:
		default:
			return
		}
	}
}
