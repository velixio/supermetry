package platformhttp

import (
	"bytes"
	"encoding/json"
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

const (
	maxClickLimit    = 1_000_000_000
	maxStatsDays     = 366
	defaultStatsDays = 30
)

// optional distinguishes an absent JSON field from an explicit null so PATCH can
// clear an expiry or a click limit.
type optional[T any] struct {
	Set   bool
	Value *T
}

func (o *optional[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		o.Value = nil
		return nil
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	o.Value = &value
	return nil
}

type linkInput struct {
	Slug        string     `json:"slug"`
	Name        string     `json:"name"`
	Destination string     `json:"destination"`
	UTMSource   string     `json:"utm_source"`
	UTMMedium   string     `json:"utm_medium"`
	UTMCampaign string     `json:"utm_campaign"`
	UTMTerm     string     `json:"utm_term"`
	UTMContent  string     `json:"utm_content"`
	Enabled     *bool      `json:"enabled"`
	ExpiresAt   *time.Time `json:"expires_at"`
	ClickLimit  *int32     `json:"click_limit"`
}

type linkPatch struct {
	Slug        *string             `json:"slug"`
	Name        *string             `json:"name"`
	Destination *string             `json:"destination"`
	UTMSource   *string             `json:"utm_source"`
	UTMMedium   *string             `json:"utm_medium"`
	UTMCampaign *string             `json:"utm_campaign"`
	UTMTerm     *string             `json:"utm_term"`
	UTMContent  *string             `json:"utm_content"`
	Enabled     *bool               `json:"enabled"`
	ExpiresAt   optional[time.Time] `json:"expires_at"`
	ClickLimit  optional[int32]     `json:"click_limit"`
}

type linkResponse struct {
	ID          int64      `json:"id"`
	SiteID      int32      `json:"site_id"`
	Slug        string     `json:"slug"`
	Name        string     `json:"name"`
	Destination string     `json:"destination"`
	UTMSource   string     `json:"utm_source"`
	UTMMedium   string     `json:"utm_medium"`
	UTMCampaign string     `json:"utm_campaign"`
	UTMTerm     string     `json:"utm_term"`
	UTMContent  string     `json:"utm_content"`
	Enabled     bool       `json:"enabled"`
	ExpiresAt   *time.Time `json:"expires_at"`
	ClickLimit  *int32     `json:"click_limit"`
	Clicks      int64      `json:"clicks"`
	Status      string     `json:"status"`
	ShortPath   string     `json:"short_path"`
	ShortURL    string     `json:"short_url"`
	CreatedBy   *int64     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func (h *handler) linkBase(r *http.Request) string {
	if base := strings.TrimRight(h.deps.BaseURL, "/"); base != "" {
		return base
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (h *handler) linkSelfHost() string {
	parsed, err := url.Parse(h.deps.BaseURL)
	if err != nil {
		return ""
	}
	return parsed.Host
}

func (h *handler) linkJSON(r *http.Request, l links.Link) linkResponse {
	path := "/l/" + l.Slug
	return linkResponse{
		ID: l.ID, SiteID: l.SiteID, Slug: l.Slug, Name: l.Name, Destination: l.Destination,
		UTMSource: l.UTM.Source, UTMMedium: l.UTM.Medium, UTMCampaign: l.UTM.Campaign, UTMTerm: l.UTM.Term, UTMContent: l.UTM.Content,
		Enabled: l.Enabled, ExpiresAt: l.ExpiresAt, ClickLimit: l.ClickLimit, Clicks: l.Clicks,
		Status: l.Status(h.deps.Now(), 0), ShortPath: path, ShortURL: h.linkBase(r) + path,
		CreatedBy: l.CreatedBy, CreatedAt: l.CreatedAt, UpdatedAt: l.UpdatedAt,
	}
}

func (h *handler) forgetLinks(slugs ...string) {
	if h.deps.LinkCache != nil {
		h.deps.LinkCache.Forget(slugs...)
	}
}

func linkRoles() []authz.Role {
	return []authz.Role{authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor, authz.RoleViewer}
}

func (h *handler) linkError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, links.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	case errors.Is(err, links.ErrSlugTaken):
		writeProblem(w, http.StatusConflict, "Conflict", "that slug is already in use")
	case errors.Is(err, links.ErrLimit):
		writeProblem(w, http.StatusForbidden, "Limit reached", fmt.Sprintf("a site can have at most %d links", links.MaxLinksPerSite))
	case errors.Is(err, links.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "link not found")
	default:
		serviceError(w, err)
	}
}

func (h *handler) listLinks(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.sitePrincipal(w, r, authz.ActionStatsRead, linkRoles()...)
	if !ok {
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "link")
		return
	}
	rows, err := links.List(r.Context(), h.deps.Store.Pool, site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	result := make([]linkResponse, 0, len(rows))
	for _, row := range rows {
		result = append(result, h.linkJSON(r, row))
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) createLink(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionLinksManage, linkRoles()...)
	if !ok {
		return
	}
	var input linkInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "link")
		return
	}
	candidate, err := h.buildLink(input, site.ID)
	if err != nil {
		h.linkError(w, err)
		return
	}
	if candidate.ExpiresAt != nil && !candidate.ExpiresAt.After(h.deps.Now()) {
		h.linkError(w, fmt.Errorf("%w: expires_at must be in the future", links.ErrInvalid))
		return
	}
	candidate.CreatedBy = &principal.UserID
	if principal.UserID == 0 {
		candidate.CreatedBy = nil
	}
	generated := input.Slug == ""
	var created links.Link
	for attempt := 0; ; attempt++ {
		if generated {
			candidate.Slug = links.RandomSlug()
		}
		err = h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
			var err error
			if created, err = links.Create(r.Context(), tx, candidate); err != nil {
				return err
			}
			return audit.Write(r.Context(), tx, principal, "link.created", "link", created.ID, map[string]any{"site_id": site.ID, "slug": created.Slug})
		})
		if generated && errors.Is(err, links.ErrSlugTaken) && attempt < 4 {
			continue
		}
		break
	}
	if err != nil {
		h.linkError(w, err)
		return
	}
	h.forgetLinks(created.Slug)
	writeJSON(w, http.StatusCreated, h.linkJSON(r, created))
}

// buildLink validates a full input into a Link ready to store.
func (h *handler) buildLink(input linkInput, siteID int32) (links.Link, error) {
	l := links.Link{SiteID: siteID, Enabled: true, ExpiresAt: input.ExpiresAt, ClickLimit: input.ClickLimit}
	var err error
	if input.Slug != "" {
		if l.Slug, err = links.NormalizeSlug(input.Slug); err != nil {
			return links.Link{}, err
		}
	}
	if l.Name, err = links.NormalizeName(input.Name); err != nil {
		return links.Link{}, err
	}
	if l.Destination, err = links.NormalizeDestination(input.Destination, h.linkSelfHost()); err != nil {
		return links.Link{}, err
	}
	if input.Enabled != nil {
		l.Enabled = *input.Enabled
	}
	if l.UTM, err = normalizeUTMSet(input.UTMSource, input.UTMMedium, input.UTMCampaign, input.UTMTerm, input.UTMContent); err != nil {
		return links.Link{}, err
	}
	if err := checkClickLimit(l.ClickLimit); err != nil {
		return links.Link{}, err
	}
	return l, nil
}

func normalizeUTMSet(source, medium, campaign, term, content string) (links.UTM, error) {
	var utm links.UTM
	var err error
	for _, field := range []struct {
		name string
		in   string
		out  *string
	}{
		{"utm_source", source, &utm.Source}, {"utm_medium", medium, &utm.Medium}, {"utm_campaign", campaign, &utm.Campaign},
		{"utm_term", term, &utm.Term}, {"utm_content", content, &utm.Content},
	} {
		if *field.out, err = links.NormalizeUTM(field.name, field.in); err != nil {
			return links.UTM{}, err
		}
	}
	return utm, nil
}

func checkClickLimit(limit *int32) error {
	if limit != nil && (*limit < 1 || *limit > maxClickLimit) {
		return fmt.Errorf("%w: click_limit must be between 1 and %d", links.ErrInvalid, maxClickLimit)
	}
	return nil
}

func (h *handler) getLink(w http.ResponseWriter, r *http.Request) {
	_, link, ok := h.linkPrincipal(w, r, authz.ActionStatsRead)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.linkJSON(r, link))
}

func (h *handler) updateLink(w http.ResponseWriter, r *http.Request) {
	principal, link, ok := h.linkPrincipal(w, r, authz.ActionLinksManage)
	if !ok {
		return
	}
	var patch linkPatch
	if !decodeJSON(w, r, &patch) {
		return
	}
	oldSlug := link.Slug
	changed := []string{}
	var err error
	set := func(name string) { changed = append(changed, name) }
	if patch.Slug != nil {
		if link.Slug, err = links.NormalizeSlug(*patch.Slug); err != nil {
			h.linkError(w, err)
			return
		}
		set("slug")
	}
	if patch.Name != nil {
		if link.Name, err = links.NormalizeName(*patch.Name); err != nil {
			h.linkError(w, err)
			return
		}
		set("name")
	}
	if patch.Destination != nil {
		if link.Destination, err = links.NormalizeDestination(*patch.Destination, h.linkSelfHost()); err != nil {
			h.linkError(w, err)
			return
		}
		set("destination")
	}
	utm := link.UTM
	for _, field := range []struct {
		name string
		in   *string
		out  *string
	}{
		{"utm_source", patch.UTMSource, &utm.Source}, {"utm_medium", patch.UTMMedium, &utm.Medium}, {"utm_campaign", patch.UTMCampaign, &utm.Campaign},
		{"utm_term", patch.UTMTerm, &utm.Term}, {"utm_content", patch.UTMContent, &utm.Content},
	} {
		if field.in == nil {
			continue
		}
		if *field.out, err = links.NormalizeUTM(field.name, *field.in); err != nil {
			h.linkError(w, err)
			return
		}
		set(field.name)
	}
	link.UTM = utm
	if patch.Enabled != nil {
		link.Enabled = *patch.Enabled
		set("enabled")
	}
	if patch.ExpiresAt.Set {
		link.ExpiresAt = patch.ExpiresAt.Value
		set("expires_at")
	}
	if patch.ClickLimit.Set {
		if err := checkClickLimit(patch.ClickLimit.Value); err != nil {
			h.linkError(w, err)
			return
		}
		link.ClickLimit = patch.ClickLimit.Value
		set("click_limit")
	}
	if len(changed) == 0 {
		writeProblem(w, http.StatusBadRequest, "Bad request", "link patch must include at least one field")
		return
	}
	var updated links.Link
	err = h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		var err error
		if updated, err = links.Update(r.Context(), tx, link); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "link.updated", "link", updated.ID, map[string]any{
			"site_id": updated.SiteID, "slug": updated.Slug, "fields": changed,
		})
	})
	if err != nil {
		h.linkError(w, err)
		return
	}
	h.forgetLinks(oldSlug, updated.Slug)
	writeJSON(w, http.StatusOK, h.linkJSON(r, updated))
}

func (h *handler) deleteLink(w http.ResponseWriter, r *http.Request) {
	principal, link, ok := h.linkPrincipal(w, r, authz.ActionLinksManage)
	if !ok {
		return
	}
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		if err := links.Delete(r.Context(), tx, link.ID); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "link.deleted", "link", link.ID, map[string]any{"site_id": link.SiteID, "slug": link.Slug})
	})
	if err != nil {
		h.linkError(w, err)
		return
	}
	h.forgetLinks(link.Slug)
	w.WriteHeader(http.StatusNoContent)
}

type linkStatsResponse struct {
	From string `json:"from"`
	To   string `json:"to"`
	links.Stats
}

func (h *handler) linkStats(w http.ResponseWriter, r *http.Request) {
	_, link, ok := h.linkPrincipal(w, r, authz.ActionStatsRead)
	if !ok {
		return
	}
	site, _ := h.deps.Sites.Site(link.SiteID)
	from, to, ok := statsRange(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	stats, err := links.ClickStats(r.Context(), h.deps.Store.Pool, link.ID, from, to)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, linkStatsResponse{From: from, To: to, Stats: stats})
}

type pixelResponse struct {
	Campaign string `json:"campaign"`
	Path     string `json:"path"`
	URL      string `json:"url"`
	HTML     string `json:"html"`
}

func (h *handler) pixelSnippet(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.sitePrincipal(w, r, authz.ActionStatsRead, linkRoles()...)
	if !ok {
		return
	}
	campaign := ""
	if raw := r.URL.Query().Get("campaign"); raw != "" {
		if campaign = links.NormalizeCampaign(raw); campaign == "" {
			writeProblem(w, http.StatusBadRequest, "Bad request", "campaign must be 1 to 64 letters, digits, dot, underscore, colon or hyphen")
			return
		}
	}
	path := "/px/" + site.PublicID + ".gif"
	if campaign != "" {
		path += "?c=" + url.QueryEscape(campaign)
	}
	full := h.linkBase(r) + path
	writeJSON(w, http.StatusOK, pixelResponse{
		Campaign: campaign, Path: path, URL: full,
		HTML: `<img src="` + html.EscapeString(full) + `" width="1" height="1" alt="" style="display:none" />`,
	})
}

type pixelStatsResponse struct {
	From string `json:"from"`
	To   string `json:"to"`
	links.PixelStats
}

func (h *handler) pixelStats(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.sitePrincipal(w, r, authz.ActionStatsRead, linkRoles()...)
	if !ok {
		return
	}
	from, to, ok := statsRange(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "link")
		return
	}
	var campaign *string
	if raw, present := r.URL.Query()["campaign"]; present && len(raw) > 0 {
		if len(raw[0]) > 64 {
			writeProblem(w, http.StatusBadRequest, "Bad request", "campaign must be at most 64 characters")
			return
		}
		value := raw[0]
		if value == links.UntaggedCampaign {
			value = ""
		}
		campaign = &value
	}
	stats, err := links.ImpressionStats(r.Context(), h.deps.Store.Pool, site.ID, from, to, campaign)
	if err != nil {
		serviceError(w, err)
		return
	}
	if stats.Summary, err = links.ImpressionSummaryFor(r.Context(), h.deps.Store.Pool, site.ID, h.deps.Now().In(siteLocation(site)).Format(time.DateOnly), campaign); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pixelStatsResponse{From: from, To: to, PixelStats: stats})
}

// statsRange parses optional from and to dates, defaulting to the last 30 days
// ending today in the site's time zone, and caps the span.
func statsRange(w http.ResponseWriter, r *http.Request, site coresites.Site, now time.Time) (string, string, bool) {
	location := site.TZ
	if location == nil {
		location = time.UTC
	}
	today := now.In(location)
	toTime := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	fromTime := toTime.AddDate(0, 0, 1-defaultStatsDays)
	var err error
	if raw := r.URL.Query().Get("to"); raw != "" {
		if toTime, err = time.Parse(time.DateOnly, raw); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad request", "to must be a date as YYYY-MM-DD")
			return "", "", false
		}
		fromTime = toTime.AddDate(0, 0, 1-defaultStatsDays)
	}
	if raw := r.URL.Query().Get("from"); raw != "" {
		if fromTime, err = time.Parse(time.DateOnly, raw); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad request", "from must be a date as YYYY-MM-DD")
			return "", "", false
		}
	}
	if fromTime.After(toTime) || toTime.Sub(fromTime) > (maxStatsDays-1)*24*time.Hour {
		writeProblem(w, http.StatusBadRequest, "Bad request", fmt.Sprintf("from and to must be in ascending order and at most %d days apart", maxStatsDays))
		return "", "", false
	}
	return fromTime.Format(time.DateOnly), toTime.Format(time.DateOnly), true
}

// linkPrincipal authenticates the caller, loads the link by its path ID and
// authorizes the action on the link's own site. A link in another organization
// or outside the caller's site grant is reported as not found.
func (h *handler) linkPrincipal(w http.ResponseWriter, r *http.Request, action authz.Action) (authz.Principal, links.Link, bool) {
	principal, ok := h.principal(w, r)
	if !ok {
		return authz.Principal{}, links.Link{}, false
	}
	id, err := parseInt64(r.PathValue("link"))
	if err != nil || h.deps.Store == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "link not found")
		return authz.Principal{}, links.Link{}, false
	}
	link, err := links.Get(r.Context(), h.deps.Store.Pool, id)
	if err != nil {
		h.linkError(w, err)
		return authz.Principal{}, links.Link{}, false
	}
	if _, _, ok := h.visibleSite(w, principal, link.SiteID); !ok {
		return authz.Principal{}, links.Link{}, false
	}
	if !roleAllowed(principal.Role, linkRoles()) || !authz.Can(principal, action, link.SiteID) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
		return authz.Principal{}, links.Link{}, false
	}
	return principal, link, true
}
