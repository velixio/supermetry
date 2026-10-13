package platformhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/clientip"
	"supermetry/internal/db"
	"supermetry/internal/platform/audit"
	platformauth "supermetry/internal/platform/auth"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/orgs"
	"supermetry/internal/platform/store"
	coresites "supermetry/internal/sites"
)

func (h *handler) currentIP(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.privatePrincipal(w, r); !ok {
		return
	}
	ip, ok := clientip.Resolve(r, h.deps.TrustedProxies)
	if !ok {
		writeProblem(w, http.StatusBadRequest, "Bad request", "client address is invalid")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ip": ip.String()})
}

func (h *handler) me(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	if h.deps.Store == nil || principal.UserID == 0 {
		unavailable(w, "user")
		return
	}
	user, err := h.deps.Store.UserByID(r.Context(), principal.UserID)
	if err != nil {
		serviceError(w, err)
		return
	}
	demoPromptDismissed, err := store.DemoPromptDismissed(r.Context(), h.deps.Store.Pool, principal.UserID)
	if err != nil {
		serviceError(w, err)
		return
	}
	if h.deps.Orgs == nil {
		unavailable(w, "organization")
		return
	}
	memberships, err := h.deps.Orgs.MembershipsFor(r.Context(), principal.UserID)
	if err != nil {
		serviceError(w, err)
		return
	}
	type membershipResponse struct {
		Org struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"org"`
		Role                  authz.Role     `json:"role"`
		SiteIDs               []int32        `json:"site_ids"`
		Capabilities          []string       `json:"capabilities"`
		BillingAdmin          bool           `json:"billing_admin"`
		EffectiveCapabilities []string       `json:"effective_capabilities"`
		Sites                 []siteResponse `json:"sites"`
	}
	items := make([]membershipResponse, 0, len(memberships))
	totalSites := 0
	for _, membership := range memberships {
		sites, err := h.membershipSites(r.Context(), membership)
		if err != nil {
			serviceError(w, err)
			return
		}
		item := membershipResponse{Role: membership.Role, SiteIDs: membership.SiteIDs, Sites: sites,
			Capabilities: membership.Capabilities, BillingAdmin: membership.BillingAdmin,
			EffectiveCapabilities: authz.EffectiveCapabilities(authz.Principal{UserID: membership.UserID, OrgID: membership.OrgID, Role: membership.Role, SiteIDs: membership.SiteIDs, Capabilities: membership.Capabilities, BillingAdmin: membership.BillingAdmin}),
		}
		item.Org.ID = membership.OrgID
		item.Org.Name = membership.OrgName
		item.Org.Slug = membership.OrgSlug
		items = append(items, item)
		totalSites += len(sites)
	}
	preferences := effectiveUserPreferences(user.Preferences)
	var twoFactorSetupRequired bool
	var twoFactorGraceUntil *time.Time
	if h.deps.Admin != nil {
		status, err := h.deps.Admin.TwoFactorStatus(r.Context(), user.ID, h.deps.Now())
		if err != nil {
			serviceError(w, err)
			return
		}
		twoFactorSetupRequired = status.SetupRequired
		twoFactorGraceUntil = status.GraceUntil
	}
	writeJSON(w, http.StatusOK, struct {
		User struct {
			ID                     int64      `json:"id"`
			Email                  string     `json:"email"`
			Name                   string     `json:"name"`
			TOTPEnabled            bool       `json:"totp_enabled"`
			CreatedAt              time.Time  `json:"created_at"`
			LastLoginAt            *time.Time `json:"last_login_at,omitempty"`
			InstanceAdmin          bool       `json:"instance_admin"`
			TwoFactorSetupRequired bool       `json:"two_factor_setup_required"`
			TwoFactorGraceUntil    *time.Time `json:"two_factor_grace_until,omitempty"`
		} `json:"user"`
		Preferences         store.UserPreferences `json:"preferences"`
		Memberships         []membershipResponse  `json:"memberships"`
		DemoPromptDismissed bool                  `json:"demo_prompt_dismissed"`
		Version             string                `json:"version"`
	}{
		User: struct {
			ID                     int64      `json:"id"`
			Email                  string     `json:"email"`
			Name                   string     `json:"name"`
			TOTPEnabled            bool       `json:"totp_enabled"`
			CreatedAt              time.Time  `json:"created_at"`
			LastLoginAt            *time.Time `json:"last_login_at,omitempty"`
			InstanceAdmin          bool       `json:"instance_admin"`
			TwoFactorSetupRequired bool       `json:"two_factor_setup_required"`
			TwoFactorGraceUntil    *time.Time `json:"two_factor_grace_until,omitempty"`
		}{user.ID, user.Email, user.Name, user.TOTPEnabled, user.CreatedAt, user.LastLoginAt, user.IsAdmin, twoFactorSetupRequired, twoFactorGraceUntil},
		Preferences:         preferences,
		Memberships:         items,
		DemoPromptDismissed: demoPromptDismissed,
		Version:             h.deps.Version,
	})
}

func effectiveUserPreferences(preferences store.UserPreferences) store.UserPreferences {
	if preferences.LandingPage == "" {
		preferences.LandingPage = "last_site"
	}
	if preferences.SitesView == "" {
		preferences.SitesView = "cards"
	}
	return preferences
}

func (h *handler) membershipSites(ctx context.Context, membership orgs.Membership) ([]siteResponse, error) {
	rows, err := h.deps.Store.Pool.Query(ctx, `
		select id,org_id,public_id,name,timezone,retention_class,allowed_hosts,
		       coalesce(settings->'exclude_ips','[]'::jsonb),coalesce(settings->'exclude_paths','[]'::jsonb),
		       coalesce(settings->>'icon_url',''),coalesce(settings->'profiles_enabled'='true'::jsonb,false),
		       coalesce(revenue_currency::text,''),revenue_currency_state,
		       is_demo,demo_seeded_at is not null,
		       (select last_event_at from site_last_event where site_id=s.id)
		from sites s
		where s.org_id=$1 and s.deleted_at is null
		  and ($2::integer[] is null or s.id=any($2))
		order by s.name,s.id`, membership.OrgID, membership.SiteIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sites := make([]siteResponse, 0)
	for rows.Next() {
		var site siteResponse
		var retention int16
		if err := rows.Scan(&site.ID, &site.OrgID, &site.PublicID, &site.Name, &site.Timezone, &retention,
			&site.AllowedHosts, &site.ExcludeIPs, &site.ExcludePaths, &site.IconURL, &site.ProfilesEnabled,
			&site.RevenueCurrency, &site.RevenueCurrencyState, &site.IsDemo, &site.DemoReady, &site.LastSeenAt); err != nil {
			return nil, err
		}
		site.RetentionDays = int(db.RetentionClass(retention))
		if site.LastSeenAt == nil {
			site.LastSeenAt, err = coresites.LastEventAt(ctx, h.deps.Store.Pool, site.ID, db.RetentionClass(retention), h.deps.Now())
			if err != nil {
				return nil, err
			}
		}
		site.TrackingPolicy = h.trackingPolicy(site.ProfilesEnabled)
		site.InstallStatus = coresites.InstallStatusAt(site.LastSeenAt, h.deps.Now())
		sites = append(sites, site)
	}
	return sites, rows.Err()
}

func (h *handler) listSessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	if h.deps.Auth == nil || principal.UserID == 0 {
		unavailable(w, "authentication")
		return
	}
	sessions, err := h.deps.Auth.Sessions(r.Context(), principal.UserID)
	if err != nil {
		serviceError(w, err)
		return
	}
	if cookie, err := r.Cookie(platformauth.CookieName); err == nil {
		for i := range sessions {
			sessions[i].Current = platformauth.IsCurrentSession(sessions[i].ID, cookie.Value)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": sessions})
}

func (h *handler) revokeSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	if h.deps.Auth == nil || principal.UserID == 0 {
		unavailable(w, "authentication")
		return
	}
	if err := h.deps.Auth.Revoke(r.Context(), principal.UserID, r.PathValue("session")); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "session is invalid")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	if h.deps.Auth == nil || principal.UserID == 0 {
		unavailable(w, "authentication")
		return
	}
	cookie, err := r.Cookie(platformauth.CookieName)
	if err != nil || cookie.Value == "" {
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "authentication is required")
		return
	}
	if err := h.deps.Auth.RevokeOthers(r.Context(), principal.UserID, cookie.Value); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) enrollTOTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	if h.deps.Auth == nil || principal.UserID == 0 {
		unavailable(w, "authentication")
		return
	}
	var request struct {
		CurrentPassword string `json:"current_password"`
		Code            string `json:"code"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	url, recovery, err := h.deps.Auth.EnrollTOTP(h.clientContext(r), principal.UserID, request.CurrentPassword, request.Code)
	if err != nil {
		writeTOTPReauthenticationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"url": url, "recovery_codes": recovery})
}

func (h *handler) confirmTOTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	var request struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Auth == nil || principal.UserID == 0 {
		unavailable(w, "authentication")
		return
	}
	if err := h.deps.Auth.ConfirmTOTP(r.Context(), principal.UserID, request.Code); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "verification code is invalid")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) disableTOTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	var request struct {
		CurrentPassword string `json:"current_password"`
		Code            string `json:"code"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Auth == nil || principal.UserID == 0 {
		unavailable(w, "authentication")
		return
	}
	if err := h.deps.Auth.DisableTOTP(h.clientContext(r), principal.UserID, request.CurrentPassword, request.Code); err != nil {
		writeTOTPReauthenticationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) regenerateTOTPRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	var request struct {
		CurrentPassword string `json:"current_password"`
		Code            string `json:"code"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Auth == nil || principal.UserID == 0 {
		unavailable(w, "authentication")
		return
	}
	recovery, err := h.deps.Auth.RegenerateTOTPRecoveryCodes(h.clientContext(r), principal.UserID, request.CurrentPassword, request.Code)
	if err != nil {
		writeTOTPReauthenticationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": recovery})
}

func writeTOTPReauthenticationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, platformauth.ErrRateLimited):
		rateLimited(w)
	case errors.Is(err, platformauth.ErrTOTPRequired):
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "a current authentication or recovery code is required")
	case errors.Is(err, platformauth.ErrInvalid):
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "current password or authentication code is invalid")
	default:
		serviceError(w, err)
	}
}

func (h *handler) listOrgs(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	if h.deps.Orgs == nil || principal.UserID == 0 {
		unavailable(w, "organization")
		return
	}
	memberships, err := h.deps.Orgs.MembershipsFor(r.Context(), principal.UserID)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": memberships})
}

const maxOrgNameRunes = 80

// createOrg lets a signed-in user start another organization; they become its
// owner. The new organization has no sites until the owner adds one.
func (h *handler) createOrg(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	var request struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	name := strings.TrimSpace(request.Name)
	if name == "" || utf8.RuneCountInString(name) > maxOrgNameRunes {
		writeProblem(w, http.StatusBadRequest, "Bad request", "name must be 1 to 80 characters")
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "organization")
		return
	}
	if h.deps.MaxOrgsPerUser > 0 {
		var admin bool
		var owned int
		if err := h.deps.Store.Pool.QueryRow(r.Context(), `
			select u.is_admin,
			       (select count(*) from memberships m join orgs o on o.id=m.org_id
			         where m.user_id=u.id and m.role='owner' and o.deleted_at is null)
			from users u where u.id=$1`, principal.UserID).Scan(&admin, &owned); err != nil {
			serviceError(w, err)
			return
		}
		if !admin && owned >= h.deps.MaxOrgsPerUser {
			writeProblem(w, http.StatusForbidden, "Limit reached", fmt.Sprintf("you already own %d organizations; ask an instance admin to add more", owned))
			return
		}
	}
	var created struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		id, err := store.CreateOrg(r.Context(), tx, name, principal.UserID)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(r.Context(), `select id,name,slug from orgs where id=$1`, id).Scan(&created.ID, &created.Name, &created.Slug); err != nil {
			return err
		}
		principal.OrgID = id
		return audit.Write(r.Context(), tx, principal, "org.created", "org", id, map[string]any{"name": name})
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *handler) getOrg(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	orgID, ok := orgIDFromPath(w, r, principal)
	if !ok {
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "organization")
		return
	}
	var response struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := h.deps.Store.Pool.QueryRow(r.Context(), `select id,name,slug from orgs where id=$1 and deleted_at is null`, orgID).Scan(&response.ID, &response.Name, &response.Slug); errors.Is(err, pgx.ErrNoRows) {
		writeProblem(w, http.StatusNotFound, "Not found", "organization not found")
		return
	} else if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *handler) listMembers(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	orgID, ok := orgIDFromPath(w, r, principal)
	if !ok {
		return
	}
	if !authz.Can(principal, authz.ActionMembersManage, 0) || h.deps.Store == nil {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot manage members")
		return
	}
	rows, err := h.deps.Store.Pool.Query(r.Context(), `select u.id,u.email,u.name,m.role,m.site_ids,m.capabilities,m.billing_admin,m.created_at from memberships m join users u on u.id=m.user_id where m.org_id=$1 order by m.created_at,u.id`, orgID)
	if err != nil {
		serviceError(w, err)
		return
	}
	defer rows.Close()
	type member struct {
		UserID       int64      `json:"user_id"`
		Email        string     `json:"email"`
		Name         string     `json:"name"`
		Role         authz.Role `json:"role"`
		SiteIDs      []int32    `json:"site_ids"`
		Capabilities []string   `json:"capabilities"`
		BillingAdmin bool       `json:"billing_admin"`
		CreatedAt    time.Time  `json:"created_at"`
	}
	members := make([]member, 0)
	for rows.Next() {
		var item member
		if err := rows.Scan(&item.UserID, &item.Email, &item.Name, &item.Role, &item.SiteIDs, &item.Capabilities, &item.BillingAdmin, &item.CreatedAt); err != nil {
			serviceError(w, err)
			return
		}
		members = append(members, item)
	}
	if err := rows.Err(); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": members})
}

func (h *handler) updateMember(w http.ResponseWriter, r *http.Request) {
	principal, userID, ok := h.memberMutation(w, r)
	if !ok {
		return
	}
	var request struct {
		Role         *orgs.Role      `json:"role"`
		SiteIDs      json.RawMessage `json:"site_ids"`
		Capabilities json.RawMessage `json:"capabilities"`
		BillingAdmin *bool           `json:"billing_admin"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Orgs == nil {
		unavailable(w, "organization")
		return
	}
	update := orgs.AccessUpdate{Role: request.Role, BillingAdmin: request.BillingAdmin}
	if len(request.SiteIDs) != 0 {
		var sites []int32
		if err := json.Unmarshal(request.SiteIDs, &sites); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad request", "site_ids must be an array or null")
			return
		}
		update.SiteIDs = &sites
	}
	if len(request.Capabilities) != 0 {
		var capabilities []string
		if err := json.Unmarshal(request.Capabilities, &capabilities); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad request", "capabilities must be an array or null")
			return
		}
		update.Capabilities = &capabilities
	}
	if err := h.deps.Orgs.UpdateAccess(r.Context(), principal, userID, update); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) deleteMember(w http.ResponseWriter, r *http.Request) {
	principal, userID, ok := h.memberMutation(w, r)
	if !ok {
		return
	}
	if h.deps.Orgs == nil {
		unavailable(w, "organization")
		return
	}
	if err := h.deps.Orgs.Remove(r.Context(), principal, userID); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) memberMutation(w http.ResponseWriter, r *http.Request) (authz.Principal, int64, bool) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return authz.Principal{}, 0, false
	}
	if _, ok := orgIDFromPath(w, r, principal); !ok {
		return authz.Principal{}, 0, false
	}
	if !authz.Can(principal, authz.ActionMembersManage, 0) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot manage members")
		return authz.Principal{}, 0, false
	}
	userID, err := parseInt64(r.PathValue("user"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "member not found")
		return authz.Principal{}, 0, false
	}
	return principal, userID, true
}
