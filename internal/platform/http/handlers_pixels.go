package platformhttp

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/pixels"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	coresites "supermetry/internal/sites"
)

type pixelInput struct {
	Name       *string `json:"name"`
	BadgeLabel *string `json:"badge_label"`
	Enabled    *bool   `json:"enabled"`
}

type pixelJSON struct {
	pixels.Pixel
	Total    int64  `json:"total"`
	Views30d int64  `json:"views_30d"`
	GIFURL   string `json:"gif_url"`
	SVGURL   string `json:"svg_url"`
}

func (h *handler) pixelSite(w http.ResponseWriter, r *http.Request, action authz.Action) (authz.Principal, coresites.Site, bool) {
	principal, site, ok := h.sitePrincipal(w, r, action, linkRoles()...)
	if !ok {
		return principal, site, false
	}
	if site.Kind != coresites.KindPixel || h.deps.Store == nil || h.deps.Pixels == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "not a pixel project")
		return principal, site, false
	}
	return principal, site, true
}

func (h *handler) toPixelJSON(r *http.Request, p pixels.Pixel, views30 map[int64]int64) pixelJSON {
	base := h.linkBase(r) + "/px/" + p.PublicID
	return pixelJSON{Pixel: p, Total: h.deps.Pixels.Totals().Get(p.ID), Views30d: views30[p.ID], GIFURL: base + ".gif", SVGURL: base + ".svg"}
}

func applyPixelInput(p *pixels.Pixel, in pixelInput) error {
	if in.Name != nil {
		p.Name = strings.TrimSpace(*in.Name)
	}
	if in.BadgeLabel != nil {
		p.BadgeLabel = strings.TrimSpace(*in.BadgeLabel)
		if n := len([]rune(p.BadgeLabel)); n < 1 || n > 40 {
			return errors.New("badge_label must be 1 to 40 characters")
		}
	}
	if in.Enabled != nil {
		p.Enabled = *in.Enabled
	}
	if n := len([]rune(p.Name)); n < 1 || n > 120 {
		return errors.New("name must be 1 to 120 characters")
	}
	return nil
}

func (h *handler) listPixels(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.pixelSite(w, r, authz.ActionStatsRead)
	if !ok {
		return
	}
	rows, err := pixels.List(r.Context(), h.deps.Store.Pool, site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	views30, err := pixels.Views30(r.Context(), h.deps.Store.Pool, site.ID, h.deps.Now().In(siteLocation(site)).Format(time.DateOnly))
	if err != nil {
		serviceError(w, err)
		return
	}
	out := make([]pixelJSON, 0, len(rows))
	for _, p := range rows {
		out = append(out, h.toPixelJSON(r, p, views30))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) createPixel(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.pixelSite(w, r, authz.ActionLinksManage)
	if !ok {
		return
	}
	var in pixelInput
	if !decodeJSON(w, r, &in) {
		return
	}
	p := pixels.Pixel{SiteID: site.ID, Enabled: true}
	if err := applyPixelInput(&p, in); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
		return
	}
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		var err error
		if p, err = pixels.Create(r.Context(), tx, p); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "pixel.created", "pixel", p.ID, map[string]any{"site_id": site.ID})
	})
	if errors.Is(err, pixels.ErrLimit) {
		writeProblem(w, http.StatusConflict, "Conflict", "a project can have at most 100 pixels")
		return
	}
	if err != nil {
		serviceError(w, err)
		return
	}
	if err := h.reloadPixels(r); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, h.toPixelJSON(r, p, nil))
}

func (h *handler) updatePixel(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.pixelSite(w, r, authz.ActionLinksManage)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeProblem(w, http.StatusBadRequest, "Bad request", "id must be a positive integer")
		return
	}
	var in pixelInput
	if !decodeJSON(w, r, &in) {
		return
	}
	var p pixels.Pixel
	err = h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		list, err := pixels.List(r.Context(), tx, site.ID)
		if err != nil {
			return err
		}
		for _, candidate := range list {
			if candidate.ID == id {
				p = candidate
			}
		}
		if p.ID == 0 {
			return pixels.ErrNotFound
		}
		if err := applyPixelInput(&p, in); err != nil {
			return errBadPixel{err}
		}
		if p, err = pixels.Update(r.Context(), tx, p); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "pixel.updated", "pixel", p.ID, map[string]any{"site_id": site.ID})
	})
	var bad errBadPixel
	switch {
	case errors.As(err, &bad):
		writeProblem(w, http.StatusBadRequest, "Bad request", bad.Error())
	case errors.Is(err, pixels.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "pixel not found")
	case err != nil:
		serviceError(w, err)
	default:
		if err := h.reloadPixels(r); err != nil {
			serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h.toPixelJSON(r, p, nil))
	}
}

type errBadPixel struct{ error }

func (h *handler) deletePixel(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.pixelSite(w, r, authz.ActionLinksManage)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeProblem(w, http.StatusBadRequest, "Bad request", "id must be a positive integer")
		return
	}
	err = h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		if err := pixels.Delete(r.Context(), tx, site.ID, id); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "pixel.deleted", "pixel", id, map[string]any{"site_id": site.ID})
	})
	if errors.Is(err, pixels.ErrNotFound) {
		writeProblem(w, http.StatusNotFound, "Not found", "pixel not found")
		return
	}
	if err != nil {
		serviceError(w, err)
		return
	}
	if err := h.reloadPixels(r); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) pixelProjectStats(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.pixelSite(w, r, authz.ActionStatsRead)
	if !ok {
		return
	}
	from, to, ok := statsRange(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	var pixelID int64
	if raw := r.URL.Query().Get("pixel"); raw != "" {
		var err error
		if pixelID, err = strconv.ParseInt(raw, 10, 64); err != nil || pixelID <= 0 {
			writeProblem(w, http.StatusBadRequest, "Bad request", "pixel must be a positive integer")
			return
		}
	}
	stats, err := pixels.StatsFor(r.Context(), h.deps.Store.Pool, site.ID, from, to, pixelID)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "daily": stats.Daily, "pixels": stats.Pixels, "countries": stats.Countries})
}

func (h *handler) reloadPixels(r *http.Request) error {
	return h.deps.Pixels.Reload(r.Context())
}

func siteLocation(site coresites.Site) *time.Location {
	if site.TZ == nil {
		return time.UTC
	}
	return site.TZ
}
