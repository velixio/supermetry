package platformhttp

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/links"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	coresites "supermetry/internal/sites"
)

type trackingPixelInput struct {
	Name string `json:"name"`
	Tag  string `json:"tag,omitempty"`
}

type trackingPixelPatch struct {
	Name *string `json:"name"`
}

type trackingPixelResponse struct {
	ID        int64     `json:"id"`
	SiteID    int32     `json:"site_id"`
	Name      string    `json:"name"`
	Tag       string    `json:"tag"`
	GIFURL    string    `json:"gif_url"`
	SVGURL    string    `json:"svg_url"`
	HTML      string    `json:"html"`
	Total     int64     `json:"total"`
	Views30D  int64     `json:"views_30d"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *handler) trackingPixelJSON(r *http.Request, site coresites.Site, pixel links.TrackingPixel) trackingPixelResponse {
	query := "?c=" + url.QueryEscape(pixel.Tag)
	gifURL := h.linkBase(r) + "/px/" + site.PublicID + ".gif" + query
	return trackingPixelResponse{
		ID: pixel.ID, SiteID: pixel.SiteID, Name: pixel.Name, Tag: pixel.Tag,
		GIFURL: gifURL, SVGURL: h.linkBase(r) + "/px/" + site.PublicID + ".svg" + query,
		HTML:  `<img src="` + html.EscapeString(gifURL) + `" width="1" height="1" alt="" style="display:none" />`,
		Total: pixel.Total, Views30D: pixel.Views30D, CreatedAt: pixel.CreatedAt,
	}
}

func (h *handler) trackingPixelError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, links.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	case errors.Is(err, links.ErrTagTaken):
		writeProblem(w, http.StatusConflict, "Conflict", "that tracking pixel tag is already in use")
	case errors.Is(err, links.ErrTrackingPixelNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "tracking pixel not found")
	default:
		serviceError(w, err)
	}
}

func (h *handler) trackingPixelToday(site coresites.Site) string {
	return h.deps.Now().In(siteLocation(site)).Format(time.DateOnly)
}

func (h *handler) listTrackingPixels(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.sitePrincipal(w, r, authz.ActionStatsRead, linkRoles()...)
	if !ok {
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "tracking pixel")
		return
	}
	pixels, err := links.ListTrackingPixels(r.Context(), h.deps.Store.Pool, site.ID, h.trackingPixelToday(site))
	if err != nil {
		serviceError(w, err)
		return
	}
	result := make([]trackingPixelResponse, 0, len(pixels))
	for _, pixel := range pixels {
		result = append(result, h.trackingPixelJSON(r, site, pixel))
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) createTrackingPixel(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionLinksManage, linkRoles()...)
	if !ok {
		return
	}
	var input trackingPixelInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "tracking pixel")
		return
	}
	name, err := links.NormalizeName(input.Name)
	if err != nil {
		h.trackingPixelError(w, err)
		return
	}
	rawTag := strings.TrimSpace(input.Tag)
	generated := rawTag == ""
	if !generated && links.NormalizeCampaign(rawTag) == "" {
		h.trackingPixelError(w, fmt.Errorf("%w: tag must be 1 to 64 letters, digits, dot, underscore, colon or hyphen", links.ErrInvalid))
		return
	}
	candidate := links.TrackingPixel{SiteID: site.ID, Name: name, Tag: rawTag}
	var created links.TrackingPixel
	for attempt := 0; ; attempt++ {
		if generated {
			candidate.Tag = links.RandomCampaignTag()
		}
		err = h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
			var err error
			if created, err = links.CreateTrackingPixel(r.Context(), tx, candidate); err != nil {
				return err
			}
			return audit.Write(r.Context(), tx, principal, "tracking_pixel.created", "tracking_pixel", created.ID, map[string]any{"site_id": site.ID, "tag": created.Tag})
		})
		if generated && errors.Is(err, links.ErrTagTaken) && attempt < 4 {
			continue
		}
		break
	}
	if err != nil {
		h.trackingPixelError(w, err)
		return
	}
	if h.deps.TrackingPixelViews != nil {
		h.deps.TrackingPixelViews.Register(created.SiteID, created.Tag)
		if err := h.deps.TrackingPixelViews.Reload(r.Context(), h.deps.Store.Pool); err != nil {
			serviceError(w, err)
			return
		}
	}
	created, err = links.GetTrackingPixel(r.Context(), h.deps.Store.Pool, created.ID, h.trackingPixelToday(site))
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, h.trackingPixelJSON(r, site, created))
}

func (h *handler) getTrackingPixel(w http.ResponseWriter, r *http.Request) {
	_, site, pixel, ok := h.trackingPixelPrincipal(w, r, authz.ActionStatsRead)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.trackingPixelJSON(r, site, pixel))
}

func (h *handler) updateTrackingPixel(w http.ResponseWriter, r *http.Request) {
	principal, site, pixel, ok := h.trackingPixelPrincipal(w, r, authz.ActionLinksManage)
	if !ok {
		return
	}
	var patch trackingPixelPatch
	if !decodeJSON(w, r, &patch) {
		return
	}
	if patch.Name == nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "tracking pixel patch must include name")
		return
	}
	name, err := links.NormalizeName(*patch.Name)
	if err != nil {
		h.trackingPixelError(w, err)
		return
	}
	var updated links.TrackingPixel
	err = h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		var err error
		if updated, err = links.RenameTrackingPixel(r.Context(), tx, pixel.ID, name); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "tracking_pixel.updated", "tracking_pixel", updated.ID, map[string]any{"site_id": updated.SiteID, "tag": updated.Tag, "fields": []string{"name"}})
	})
	if err != nil {
		h.trackingPixelError(w, err)
		return
	}
	updated.Total, updated.Views30D = pixel.Total, pixel.Views30D
	writeJSON(w, http.StatusOK, h.trackingPixelJSON(r, site, updated))
}

func (h *handler) deleteTrackingPixel(w http.ResponseWriter, r *http.Request) {
	principal, _, pixel, ok := h.trackingPixelPrincipal(w, r, authz.ActionLinksManage)
	if !ok {
		return
	}
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		if err := links.DeleteTrackingPixel(r.Context(), tx, pixel.ID); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "tracking_pixel.deleted", "tracking_pixel", pixel.ID, map[string]any{"site_id": pixel.SiteID, "tag": pixel.Tag})
	})
	if err != nil {
		h.trackingPixelError(w, err)
		return
	}
	if h.deps.TrackingPixelViews != nil {
		h.deps.TrackingPixelViews.Unregister(pixel.SiteID, pixel.Tag)
		if err := h.deps.TrackingPixelViews.Reload(r.Context(), h.deps.Store.Pool); err != nil {
			serviceError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) trackingPixelPrincipal(w http.ResponseWriter, r *http.Request, action authz.Action) (authz.Principal, coresites.Site, links.TrackingPixel, bool) {
	principal, ok := h.principal(w, r)
	if !ok {
		return authz.Principal{}, coresites.Site{}, links.TrackingPixel{}, false
	}
	id, err := parseInt64(r.PathValue("id"))
	if err != nil || h.deps.Store == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "tracking pixel not found")
		return authz.Principal{}, coresites.Site{}, links.TrackingPixel{}, false
	}
	pixel, err := links.GetTrackingPixel(r.Context(), h.deps.Store.Pool, id, h.deps.Now().UTC().Format(time.DateOnly))
	if err != nil {
		h.trackingPixelError(w, err)
		return authz.Principal{}, coresites.Site{}, links.TrackingPixel{}, false
	}
	_, site, ok := h.visibleSite(w, principal, pixel.SiteID)
	if !ok {
		return authz.Principal{}, coresites.Site{}, links.TrackingPixel{}, false
	}
	if !roleAllowed(principal.Role, linkRoles()) || !authz.Can(principal, action, pixel.SiteID) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
		return authz.Principal{}, coresites.Site{}, links.TrackingPixel{}, false
	}
	pixel, err = links.GetTrackingPixel(r.Context(), h.deps.Store.Pool, id, h.trackingPixelToday(site))
	if err != nil {
		h.trackingPixelError(w, err)
		return authz.Principal{}, coresites.Site{}, links.TrackingPixel{}, false
	}
	return principal, site, pixel, true
}
