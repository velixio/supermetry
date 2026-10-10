package sites

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"supermetry/internal/db"
)

// FileRegistry is an immutable site registry loaded from a JSON file.
type FileRegistry struct {
	byPublicID map[string]Site
	byID       map[int32]Site
	sites      []Site
}

type fileSite struct {
	ID                    int32                `json:"id"`
	OrgID                 int64                `json:"org_id"`
	PublicID              string               `json:"public_id"`
	Timezone              string               `json:"timezone"`
	RetentionDays         int16                `json:"retention_days"`
	AllowedHosts          []string             `json:"allowed_hosts"`
	ProfilesEnabled       bool                 `json:"profiles_enabled"`
	HeatmapsEnabled       bool                 `json:"heatmaps_enabled"`
	RespectPrivacySignals *bool                `json:"respect_privacy_signals"`
	StrictOrigin          bool                 `json:"strict_origin"`
	BlockedReferrers      []string             `json:"blocked_referrers"`
	Replay                json.RawMessage      `json:"replay"`
	RevenueCurrency       string               `json:"revenue_currency"`
	RevenueCurrencyState  RevenueCurrencyState `json:"revenue_currency_state"`
	Demo                  bool                 `json:"demo"`
}

// LoadFile loads site metadata from a JSON array at path.
func LoadFile(path string) (*FileRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read sites file: %w", err)
	}
	var records []fileSite
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("decode sites file: %w", err)
	}

	r := &FileRegistry{
		byPublicID: make(map[string]Site, len(records)),
		byID:       make(map[int32]Site, len(records)),
		sites:      make([]Site, 0, len(records)),
	}
	for i, record := range records {
		location, err := time.LoadLocation(record.Timezone)
		if err != nil {
			return nil, fmt.Errorf("site %d timezone: %w", i, err)
		}
		retention, ok := retentionClass(record.RetentionDays)
		if !ok {
			return nil, fmt.Errorf("site %d: unsupported retention_days %d", i, record.RetentionDays)
		}
		allowedHosts := make([]string, len(record.AllowedHosts))
		for j, host := range record.AllowedHosts {
			allowedHosts[j] = strings.ToLower(host)
		}
		respectPrivacySignals := true
		if record.RespectPrivacySignals != nil {
			respectPrivacySignals = *record.RespectPrivacySignals
		}
		replaySettings, err := ParseReplaySiteInput(record.Replay)
		if err != nil {
			return nil, fmt.Errorf("site %d replay settings: %w", i, err)
		}
		blockedReferrers, err := NormalizeBlockedReferrers(record.BlockedReferrers, MaxBlockedReferrers)
		if err != nil {
			return nil, fmt.Errorf("site %d blocked referrers: %w", i, err)
		}
		site := Site{
			OrgID:                 record.OrgID,
			ID:                    record.ID,
			PublicID:              record.PublicID,
			TZ:                    location,
			Retention:             retention,
			AllowedHosts:          allowedHosts,
			ProfilesEnabled:       record.ProfilesEnabled,
			HeatmapsEnabled:       record.HeatmapsEnabled,
			RespectPrivacySignals: respectPrivacySignals,
			StrictOrigin:          record.StrictOrigin,
			BlockedReferrers:      blockedReferrers,
			Replay:                replaySettings,
			RevenueCurrency:       record.RevenueCurrency,
			RevenueCurrencyState:  record.RevenueCurrencyState,
			Demo:                  record.Demo,
		}
		if _, exists := r.byPublicID[site.PublicID]; exists {
			return nil, fmt.Errorf("site %d: duplicate public_id %q", i, site.PublicID)
		}
		if _, exists := r.byID[site.ID]; exists {
			return nil, fmt.Errorf("site %d: duplicate id %d", i, site.ID)
		}
		r.byPublicID[site.PublicID] = site
		r.byID[site.ID] = site
		r.sites = append(r.sites, site)
	}
	return r, nil
}

func retentionClass(days int16) (db.RetentionClass, bool) {
	switch days {
	case 0:
		return db.RetForever, true
	case 7:
		return db.Ret7, true
	case 30:
		return db.Ret30, true
	case 90:
		return db.Ret90, true
	case 365:
		return db.Ret365, true
	default:
		return 0, false
	}
}

// ByPublicID returns the site with the given public identifier.
func (r *FileRegistry) ByPublicID(id string) (Site, bool) {
	site, ok := r.byPublicID[id]
	return cloneFileSite(site), ok
}

// Site returns the site with the given internal identifier.
func (r *FileRegistry) Site(id int32) (Site, bool) {
	site, ok := r.byID[id]
	return cloneFileSite(site), ok
}

// All returns every configured site in file order.
func (r *FileRegistry) All() []Site {
	result := make([]Site, len(r.sites))
	for i, site := range r.sites {
		result[i] = cloneFileSite(site)
	}
	return result
}

func cloneFileSite(site Site) Site {
	site.AllowedHosts = append([]string(nil), site.AllowedHosts...)
	site.BlockedReferrers = append([]string(nil), site.BlockedReferrers...)
	site.Exclusions = site.Exclusions.Clone()
	site.Replay = cloneReplaySiteInput(site.Replay)
	return site
}

func cloneReplaySiteInput(settings ReplaySiteInput) ReplaySiteInput {
	cloneBool := func(value *bool) *bool {
		if value == nil {
			return nil
		}
		copied := *value
		return &copied
	}
	cloneInt := func(value *int) *int {
		if value == nil {
			return nil
		}
		copied := *value
		return &copied
	}
	cloneInt64 := func(value *int64) *int64 {
		if value == nil {
			return nil
		}
		copied := *value
		return &copied
	}
	cloneString := func(value *string) *string {
		if value == nil {
			return nil
		}
		copied := *value
		return &copied
	}
	settings.Mask = cloneBool(settings.Mask)
	settings.SampleRate = cloneInt(settings.SampleRate)
	settings.RetentionDays = cloneInt(settings.RetentionDays)
	settings.QuotaBytes = cloneInt64(settings.QuotaBytes)
	settings.Consent = cloneString(settings.Consent)
	settings.Triggers.Error = cloneBool(settings.Triggers.Error)
	settings.Triggers.RageClick = cloneBool(settings.Triggers.RageClick)
	settings.Triggers.URL = cloneBool(settings.Triggers.URL)
	settings.Triggers.Manual = cloneBool(settings.Triggers.Manual)
	settings.Triggers.URLPatterns = append([]string(nil), settings.Triggers.URLPatterns...)
	return settings
}
