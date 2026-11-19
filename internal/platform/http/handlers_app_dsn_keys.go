package platformhttp

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"supermetry/internal/platform/appkeys"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/store"

	"github.com/jackc/pgx/v5"
)

const (
	maxDSNOrigins = 20
	maxDSNLabel   = 80
)

type dsnKeyBody struct {
	Label          string     `json:"label"`
	AllowedOrigins []string   `json:"allowed_origins"`
	ExpiresAt      *time.Time `json:"expires_at"`
}

type dsnKeyUpdateBody struct {
	Label          *string   `json:"label"`
	AllowedOrigins *[]string `json:"allowed_origins"`
}

type dsnKeyItem struct {
	PublicKey      string     `json:"public_key"`
	Prefix         string     `json:"prefix"`
	DSN            string     `json:"dsn"`
	Label          string     `json:"label"`
	AllowedOrigins []string   `json:"allowed_origins"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      *time.Time `json:"expires_at"`
}

// dsnFor builds https://<key>@<host>/<site id> from the configured public base
// URL; it is empty when no base URL is configured.
func (h *handler) dsnFor(publicKey string, siteID int32) string {
	base, err := url.Parse(strings.TrimRight(h.deps.BaseURL, "/"))
	if err != nil || base.Host == "" || base.Scheme == "" {
		return ""
	}
	dsn := url.URL{Scheme: base.Scheme, User: url.User(publicKey), Host: base.Host, Path: strings.TrimRight(base.Path, "/") + "/" + strconv.Itoa(int(siteID))}
	return dsn.String()
}

// normalizeOrigins lower-cases and validates scheme://host[:port] origins.
func normalizeOrigins(in []string) ([]string, bool) {
	if len(in) > maxDSNOrigins {
		return nil, false
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		origin := strings.ToLower(strings.TrimSpace(raw))
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
			parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
			return nil, false
		}
		if _, dup := seen[origin]; dup {
			continue
		}
		seen[origin] = struct{}{}
		out = append(out, origin)
	}
	return out, true
}

func (h *handler) listAppDSNKeys(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.appSite(w, r)
	if !ok {
		return
	}
	rows, err := h.deps.Store.ListDSNKeys(r.Context(), site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	keys := make([]dsnKeyItem, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, h.dsnItem(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (h *handler) dsnItem(row store.DSNKeyRow) dsnKeyItem {
	origins := row.AllowedOrigins
	if origins == nil {
		origins = []string{}
	}
	return dsnKeyItem{PublicKey: row.PublicKey, Prefix: row.PublicKey[:8], DSN: h.dsnFor(row.PublicKey, row.SiteID),
		Label: row.Label, AllowedOrigins: origins, CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt}
}

func (h *handler) createAppDSNKey(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appSite(w, r)
	if !ok {
		return
	}
	var in dsnKeyBody
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "request body too large or unreadable")
		return
	}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad request", "invalid JSON body")
			return
		}
	}
	origins, valid := normalizeOrigins(in.AllowedOrigins)
	if !valid {
		writeProblem(w, http.StatusBadRequest, "Bad request", "allowed_origins must be at most 20 scheme://host[:port] origins")
		return
	}
	label := strings.TrimSpace(in.Label)
	if len(label) > maxDSNLabel {
		writeProblem(w, http.StatusBadRequest, "Bad request", "label is too long")
		return
	}
	now := h.deps.Now()
	if in.ExpiresAt != nil && !in.ExpiresAt.After(now) {
		writeProblem(w, http.StatusBadRequest, "Bad request", "expires_at must be in the future")
		return
	}
	var createdBy *int64
	if principal.UserID != 0 {
		createdBy = &principal.UserID
	}
	row := store.DSNKeyRow{SiteID: site.ID, PublicKey: appkeys.NewDSNKey(), Label: label, AllowedOrigins: origins, CreatedAt: now, ExpiresAt: in.ExpiresAt}
	err = h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		if err := h.deps.Store.CreateDSNKeyTx(r.Context(), tx, row, createdBy); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "site.dsn_key_created", "site", int64(site.ID), map[string]any{"prefix": row.PublicKey[:8]})
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	h.refreshSites(site.ID, "dsn key creation")
	writeJSON(w, http.StatusCreated, h.dsnItem(row))
}

func (h *handler) revokeAppDSNKey(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appSite(w, r)
	if !ok {
		return
	}
	prefix := r.PathValue("prefix")
	if len(prefix) != 8 {
		writeProblem(w, http.StatusNotFound, "Not found", "no such DSN key")
		return
	}
	var found bool
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		var err error
		if found, err = h.deps.Store.RevokeDSNKeyTx(r.Context(), tx, site.ID, prefix, h.deps.Now()); err != nil || !found {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "site.dsn_key_revoked", "site", int64(site.ID), map[string]any{"prefix": prefix})
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	if !found {
		writeProblem(w, http.StatusNotFound, "Not found", "no such DSN key")
		return
	}
	h.refreshSites(site.ID, "dsn key revocation")
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) updateAppDSNKey(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appSite(w, r)
	if !ok {
		return
	}
	prefix := r.PathValue("prefix")
	if len(prefix) != 8 {
		writeProblem(w, http.StatusNotFound, "Not found", "no such DSN key")
		return
	}
	var in dsnKeyUpdateBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "invalid JSON body")
		return
	}
	if in.AllowedOrigins == nil && in.Label == nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "send allowed_origins, label or both")
		return
	}
	fields := []string{}
	audited := map[string]any{"prefix": prefix}
	var origins *[]string
	if in.AllowedOrigins != nil {
		normalized, valid := normalizeOrigins(*in.AllowedOrigins)
		if !valid {
			writeProblem(w, http.StatusBadRequest, "Bad request", "allowed_origins must be at most 20 scheme://host[:port] origins")
			return
		}
		origins = &normalized
		fields = append(fields, "allowed_origins")
		audited["origin_count"] = len(normalized)
	}
	var label *string
	if in.Label != nil {
		trimmed := strings.TrimSpace(*in.Label)
		if len(trimmed) > maxDSNLabel {
			writeProblem(w, http.StatusBadRequest, "Bad request", "label is too long")
			return
		}
		label = &trimmed
		fields = append(fields, "label")
	}
	audited["fields"] = fields
	var row store.DSNKeyRow
	var found bool
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		var err error
		if row, found, err = h.deps.Store.UpdateDSNKeyTx(r.Context(), tx, site.ID, prefix, origins, label, h.deps.Now()); err != nil || !found {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "site.dsn_key_updated", "site", int64(site.ID), audited)
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	if !found {
		writeProblem(w, http.StatusNotFound, "Not found", "no such DSN key")
		return
	}
	h.refreshSites(site.ID, "dsn key update")
	writeJSON(w, http.StatusOK, h.dsnItem(row))
}

func (h *handler) refreshSites(siteID int32, what string) {
	if inv, ok := h.deps.Sites.(interface{ Invalidate() error }); ok {
		if err := inv.Invalidate(); err != nil {
			slog.Error("site registry refresh failed after "+what, "site_id", siteID, "error", err)
		}
	}
}
