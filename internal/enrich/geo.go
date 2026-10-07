package enrich

import (
	_ "embed"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/oschwald/maxminddb-golang/v2"
)

//go:embed lists/dbip-country-lite.mmdb
var embeddedGeoDB []byte

// GeoPaths names optional MaxMind-format databases used for enrichment.
type GeoPaths struct {
	Country string
	City    string
	ASN     string
}

// CityInfo contains an ISO country code and English location names associated
// with an address.
type CityInfo struct {
	Country string
	Region  string
	City    string
}

// ASNInfo contains the autonomous system associated with an address.
type ASNInfo struct {
	Number       uint32 `maxminddb:"autonomous_system_number"`
	Organization string `maxminddb:"autonomous_system_organization"`
}

type readerSet struct {
	country *maxminddb.Reader
	city    *maxminddb.Reader
	asn     *maxminddb.Reader
	owned   []*maxminddb.Reader
}

// Enricher derives non-identifying client dimensions and rejects known bots.
type Enricher struct {
	ua      *uaCache
	geo     atomic.Pointer[readerSet]
	geoLife sync.RWMutex
	closed  bool
	crawler crawlerMatcher
	spam    map[string]struct{}
	source  *SourceClassifier
}

// New loads an Enricher. An empty geoPath uses the bundled DB-IP Lite country
// database; a non-empty path accepts a compatible country-capable database.
func New(geoPath string) (*Enricher, error) {
	return NewWithGeo(GeoPaths{Country: geoPath})
}

// NewWithGeo loads an Enricher with optional country, city and ASN databases.
// City data takes precedence over country data for Country lookups. When
// neither is supplied, the bundled DB-IP Lite country database is used.
func NewWithGeo(paths GeoPaths) (*Enricher, error) {
	readers, err := openReaderSet(paths)
	if err != nil {
		return nil, err
	}

	crawler, spam, err := loadBotData()
	if err != nil {
		_ = readers.close()
		return nil, err
	}
	source, err := NewSourceClassifier()
	if err != nil {
		_ = readers.close()
		return nil, err
	}

	e := &Enricher{
		ua:      newUACache(uaCacheSize),
		crawler: crawler,
		spam:    spam,
		source:  source,
	}
	e.geo.Store(readers)
	return e, nil
}

// SwapGeo atomically publishes a fully validated replacement database set.
// The lifecycle lock lets in-flight lookups finish before old readers close.
func (e *Enricher) SwapGeo(paths GeoPaths) error {
	if e == nil {
		return errors.New("swap GeoIP databases on nil enricher")
	}
	readers, err := openReaderSet(paths)
	if err != nil {
		return err
	}

	e.geoLife.Lock()
	defer e.geoLife.Unlock()
	if e.closed {
		_ = readers.close()
		return errors.New("swap GeoIP databases on closed enricher")
	}
	previous := e.geo.Swap(readers)
	return previous.close()
}

// Close releases the active GeoIP database readers.
func (e *Enricher) Close() error {
	if e == nil {
		return nil
	}
	e.geoLife.Lock()
	defer e.geoLife.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	active := e.geo.Swap(nil)
	return active.close()
}

// Country returns an ISO 3166-1 alpha-2 country code, or an empty string when
// the address is invalid or absent from the database.
func (e *Enricher) Country(ip netip.Addr) string {
	if e == nil || !ip.IsValid() {
		return ""
	}
	e.geoLife.RLock()
	defer e.geoLife.RUnlock()
	readers := e.geo.Load()
	if readers == nil || readers.country == nil {
		return ""
	}
	var iso string
	if err := readers.country.Lookup(ip.Unmap()).DecodePath(&iso, "country", "iso_code"); err != nil {
		return ""
	}
	return iso
}

// City returns an ISO country code plus English first-level region and city
// names. Country-only databases return the code with empty location names.
func (e *Enricher) City(ip netip.Addr) CityInfo {
	if e == nil || !ip.IsValid() {
		return CityInfo{}
	}
	e.geoLife.RLock()
	defer e.geoLife.RUnlock()
	readers := e.geo.Load()
	if readers == nil || readers.country == nil {
		return CityInfo{}
	}
	var record struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
		Subdivisions []struct {
			Names map[string]string `maxminddb:"names"`
		} `maxminddb:"subdivisions,maxsize:32"`
		City struct {
			Names map[string]string `maxminddb:"names"`
		} `maxminddb:"city"`
	}
	if err := readers.country.Lookup(ip.Unmap()).Decode(&record); err != nil {
		return CityInfo{}
	}
	result := CityInfo{
		Country: record.Country.ISOCode,
		City:    record.City.Names["en"],
	}
	if len(record.Subdivisions) > 0 {
		result.Region = record.Subdivisions[0].Names["en"]
	}
	return result
}

// ASN returns the autonomous system number and organization for an address.
func (e *Enricher) ASN(ip netip.Addr) ASNInfo {
	if e == nil || !ip.IsValid() {
		return ASNInfo{}
	}
	e.geoLife.RLock()
	defer e.geoLife.RUnlock()
	readers := e.geo.Load()
	if readers == nil || readers.asn == nil {
		return ASNInfo{}
	}
	var result ASNInfo
	if err := readers.asn.Lookup(ip.Unmap()).Decode(&result); err != nil {
		return ASNInfo{}
	}
	return result
}

// GeoIPSource reports the provider represented by the active reader set.
func (e *Enricher) GeoIPSource() string {
	if e == nil {
		return "dbip"
	}
	e.geoLife.RLock()
	defer e.geoLife.RUnlock()
	readers := e.geo.Load()
	if readers == nil {
		return "dbip"
	}
	for _, reader := range readers.owned {
		if reader != nil && (strings.HasPrefix(reader.Metadata.DatabaseType, "GeoLite2-") || strings.HasPrefix(reader.Metadata.DatabaseType, "GeoIP2-")) {
			return "maxmind"
		}
	}
	return "dbip"
}

func openReaderSet(paths GeoPaths) (_ *readerSet, err error) {
	readers := &readerSet{}
	defer func() {
		if err != nil {
			_ = readers.close()
		}
	}()

	var country *maxminddb.Reader
	if paths.Country != "" {
		country, err = openGeoFile(paths.Country, "country", countryEditions)
		if err != nil {
			return nil, err
		}
		readers.owned = append(readers.owned, country)
	}
	if paths.City != "" {
		readers.city, err = openGeoFile(paths.City, "city", cityEditions)
		if err != nil {
			return nil, err
		}
		readers.owned = append(readers.owned, readers.city)
	}
	if paths.ASN != "" {
		readers.asn, err = openGeoFile(paths.ASN, "ASN", asnEditions)
		if err != nil {
			return nil, err
		}
		readers.owned = append(readers.owned, readers.asn)
	}

	switch {
	case readers.city != nil:
		readers.country = readers.city
	case country != nil:
		readers.country = country
	default:
		readers.country, err = openGeoBytes(embeddedGeoDB, "country", countryEditions)
		if err != nil {
			return nil, err
		}
		readers.owned = append(readers.owned, readers.country)
	}
	return readers, nil
}

var (
	countryEditions = map[string]struct{}{
		"DBIP-Country-Lite": {},
		"GeoIP2-City":       {},
		"GeoIP2-Country":    {},
		"GeoLite2-City":     {},
		"GeoLite2-Country":  {},
	}
	cityEditions = map[string]struct{}{
		"GeoIP2-City":   {},
		"GeoLite2-City": {},
	}
	asnEditions = map[string]struct{}{
		"GeoLite2-ASN": {},
	}
)

func openGeoFile(path, kind string, editions map[string]struct{}) (*maxminddb.Reader, error) {
	reader, err := maxminddb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s database: %w", kind, err)
	}
	return validateReader(reader, kind, editions)
}

func openGeoBytes(data []byte, kind string, editions map[string]struct{}) (*maxminddb.Reader, error) {
	reader, err := maxminddb.OpenBytes(data)
	if err != nil {
		return nil, fmt.Errorf("open %s database: %w", kind, err)
	}
	return validateReader(reader, kind, editions)
}

func validateReader(reader *maxminddb.Reader, kind string, editions map[string]struct{}) (*maxminddb.Reader, error) {
	if err := reader.Verify(); err != nil {
		_ = reader.Close()
		return nil, fmt.Errorf("verify %s database: %w", kind, err)
	}
	edition := reader.Metadata.DatabaseType
	if _, ok := editions[edition]; !ok {
		_ = reader.Close()
		return nil, fmt.Errorf("invalid %s database edition %q", kind, edition)
	}
	return reader, nil
}

func (r *readerSet) close() error {
	if r == nil {
		return nil
	}
	var errs []error
	for _, reader := range r.owned {
		if reader != nil {
			errs = append(errs, reader.Close())
		}
	}
	return errors.Join(errs...)
}
