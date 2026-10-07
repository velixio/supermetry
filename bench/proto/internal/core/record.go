package core

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Record is one line of the canonical NDJSON stream (bench/CONTRACT.md).
// The ground-truth keys vk/sk are deliberately not decoded: the prototype
// derives visitors and sessions from ip+ua+site only.
type Record struct {
	TS          time.Time       `json:"ts"`
	Site        int32           `json:"site"`
	Host        string          `json:"host"`
	Path        string          `json:"path"`
	Ref         string          `json:"ref"`
	UTMSource   string          `json:"utm_source"`
	UTMMedium   string          `json:"utm_medium"`
	UTMCampaign string          `json:"utm_campaign"`
	Name        string          `json:"name"`
	Props       json.RawMessage `json:"props"`
	IP          string          `json:"ip"`
	UA          string          `json:"ua"`
	ScreenW     int             `json:"screen_w"`
	Country     string          `json:"country"`
	Browser     string          `json:"browser"`
	OS          string          `json:"os"`
	Device      string          `json:"device"`
}

// LoadSitesFile reads the generator's sites.json ([{id, host, timezone, share}]).
func LoadSitesFile(path string) ([]Site, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sites []Site
	if err := json.Unmarshal(b, &sites); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for i := range sites {
		if sites[i].TZ == "" {
			sites[i].TZ = "UTC"
		}
		if sites[i].Loc, err = time.LoadLocation(sites[i].TZ); err != nil {
			return nil, err
		}
	}
	return sites, nil
}
