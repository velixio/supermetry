package enrich

import (
	_ "embed"
	"net/netip"

	"github.com/oschwald/maxminddb-golang/v2"
)

//go:embed lists/dbip-country-lite.mmdb
var embeddedGeoDB []byte

// Enricher derives non-identifying client dimensions and rejects known bots.
type Enricher struct {
	ua         *uaCache
	geo        *maxminddb.Reader
	crawler    crawlerMatcher
	datacenter prefixSet
	spam       map[string]struct{}
	sources    []sourceRule
}

// New loads an Enricher. An empty geoPath uses the bundled DB-IP Lite country
// database; a non-empty path allows an updated database to be supplied.
func New(geoPath string) (*Enricher, error) {
	var (
		geo *maxminddb.Reader
		err error
	)
	if geoPath == "" {
		geo, err = maxminddb.OpenBytes(embeddedGeoDB)
	} else {
		geo, err = maxminddb.Open(geoPath)
	}
	if err != nil {
		return nil, err
	}

	crawler, datacenter, spam, err := loadBotData()
	if err != nil {
		_ = geo.Close()
		return nil, err
	}
	sources, err := loadSources()
	if err != nil {
		_ = geo.Close()
		return nil, err
	}

	return &Enricher{
		ua:         newUACache(uaCacheSize),
		geo:        geo,
		crawler:    crawler,
		datacenter: datacenter,
		spam:       spam,
		sources:    sources,
	}, nil
}

// Close releases resources used by a file-backed GeoIP database.
func (e *Enricher) Close() error {
	if e == nil || e.geo == nil {
		return nil
	}
	return e.geo.Close()
}

// Country returns an ISO 3166-1 alpha-2 country code, or an empty string when
// the address is invalid or absent from the database.
func (e *Enricher) Country(ip netip.Addr) string {
	if e == nil || e.geo == nil || !ip.IsValid() {
		return ""
	}
	var iso string
	if err := e.geo.Lookup(ip.Unmap()).DecodePath(&iso, "country", "iso_code"); err != nil {
		return ""
	}
	return iso
}
