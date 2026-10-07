package sites

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"panel/internal/db"
)

// FileRegistry is an immutable site registry loaded from a JSON file.
type FileRegistry struct {
	byPublicID map[string]Site
	byID       map[int32]Site
	sites      []Site
}

type fileSite struct {
	ID            int32    `json:"id"`
	PublicID      string   `json:"public_id"`
	Timezone      string   `json:"timezone"`
	RetentionDays int16    `json:"retention_days"`
	AllowedHosts  []string `json:"allowed_hosts"`
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
		site := Site{
			ID:           record.ID,
			PublicID:     record.PublicID,
			TZ:           location,
			Retention:    retention,
			AllowedHosts: allowedHosts,
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
	return site, ok
}

// Site returns the site with the given internal identifier.
func (r *FileRegistry) Site(id int32) (Site, bool) {
	site, ok := r.byID[id]
	return site, ok
}

// All returns every configured site in file order.
func (r *FileRegistry) All() []Site {
	return append([]Site(nil), r.sites...)
}
