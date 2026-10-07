// Package enrich turns ip+UA into country/browser/os/device. The real
// enricher uses github.com/mileusna/useragent (MIT) and a MaxMind-format
// country database (DB-IP Lite, CC-BY 4.0) via
// github.com/oschwald/maxminddb-golang/v2 (ISC).
package enrich

import (
	"net/netip"
	"strings"

	"github.com/mileusna/useragent"
	"github.com/oschwald/maxminddb-golang/v2"
)

// Result of enrichment.
type Result struct {
	Country, Browser, OS, Device string
	Bot                          bool
}

// Real is the production-like enricher.
type Real struct {
	geo *maxminddb.Reader
}

// Open loads the mmdb at path. An empty path disables GeoIP (country "").
func Open(path string) (*Real, error) {
	if path == "" {
		return &Real{}, nil
	}
	db, err := maxminddb.Open(path)
	if err != nil {
		return nil, err
	}
	return &Real{geo: db}, nil
}

// Close releases the mmdb mapping.
func (r *Real) Close() error {
	if r.geo != nil {
		return r.geo.Close()
	}
	return nil
}

// Enrich parses ua and looks ip up.
func (r *Real) Enrich(ip, ua string) Result {
	p := useragent.Parse(ua)
	res := Result{Browser: browserName(p.Name), OS: p.OS, Bot: p.Bot || ua == ""}
	switch {
	case p.Tablet:
		res.Device = "tablet"
	case p.Mobile:
		res.Device = "mobile"
	case p.Desktop:
		res.Device = "desktop"
	default:
		res.Device = ""
	}
	res.Country = r.Country(ip)
	return res
}

// Country returns the ISO code for ip, or "".
func (r *Real) Country(ip string) string {
	if r.geo == nil {
		return ""
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	var iso string
	if err := r.geo.Lookup(addr).DecodePath(&iso, "country", "iso_code"); err != nil {
		return ""
	}
	return iso
}

func browserName(n string) string {
	// Fold "Mobile Safari" into "Safari" like the generator's labels; keep the rest.
	if strings.HasPrefix(n, "Mobile ") {
		return strings.TrimPrefix(n, "Mobile ")
	}
	return n
}
