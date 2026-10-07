package enrich_test

import (
	"bytes"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
	"github.com/oschwald/maxminddb-golang/v2"

	"panel/internal/enrich"
)

var testPrefix = netip.MustParsePrefix("192.0.2.0/24")

func TestNewWithGeoLookups(t *testing.T) {
	countryPath := writeMMDB(t, "GeoLite2-Country", mmdbtype.Map{
		"country": mmdbtype.Map{
			"iso_code": mmdbtype.String("CA"),
		},
	})
	cityPath := writeMMDB(t, "GeoLite2-City", mmdbtype.Map{
		"country": mmdbtype.Map{
			"iso_code": mmdbtype.String("US"),
			"names": mmdbtype.Map{
				"en": mmdbtype.String("United States"),
			},
		},
		"subdivisions": mmdbtype.Slice{mmdbtype.Map{
			"names": mmdbtype.Map{
				"en": mmdbtype.String("California"),
			},
		}},
		"city": mmdbtype.Map{
			"names": mmdbtype.Map{
				"en": mmdbtype.String("Testville"),
			},
		},
	})
	asnPath := writeMMDB(t, "GeoLite2-ASN", mmdbtype.Map{
		"autonomous_system_number":       mmdbtype.Uint32(64500),
		"autonomous_system_organization": mmdbtype.String("Example Broadband"),
	})

	e, err := enrich.NewWithGeo(enrich.GeoPaths{
		Country: countryPath,
		City:    cityPath,
		ASN:     asnPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })

	ip := netip.MustParseAddr("192.0.2.42")
	if got := e.Country(ip); got != "US" {
		t.Errorf("Country() = %q, want US from City database", got)
	}
	wantCity := enrich.CityInfo{
		Country: "US",
		Region:  "California",
		City:    "Testville",
	}
	if got := e.City(ip); got != wantCity {
		t.Errorf("City() = %+v, want %+v", got, wantCity)
	}
	wantASN := enrich.ASNInfo{Number: 64500, Organization: "Example Broadband"}
	if got := e.ASN(ip); got != wantASN {
		t.Errorf("ASN() = %+v, want %+v", got, wantASN)
	}
}

func TestNewPreservesCountryPathBehavior(t *testing.T) {
	path := writeMMDB(t, "GeoLite2-Country", mmdbtype.Map{
		"country": mmdbtype.Map{"iso_code": mmdbtype.String("NZ")},
	})
	e, err := enrich.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })

	if got := e.Country(netip.MustParseAddr("192.0.2.1")); got != "NZ" {
		t.Errorf("Country() = %q, want NZ", got)
	}
	if got := e.City(netip.MustParseAddr("192.0.2.1")); got != (enrich.CityInfo{Country: "NZ"}) {
		t.Errorf("City() = %+v, want country code with empty region and city", got)
	}
}

func TestNewPreservesCityPathCountryLookup(t *testing.T) {
	path := writeMMDB(t, "GeoLite2-City", mmdbtype.Map{
		"country": mmdbtype.Map{"iso_code": mmdbtype.String("JP")},
	})
	e, err := enrich.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })

	if got := e.Country(netip.MustParseAddr("192.0.2.1")); got != "JP" {
		t.Errorf("Country() = %q, want JP", got)
	}
}

func TestEnricherGeoIPSource(t *testing.T) {
	bundled, err := enrich.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bundled.Close() })
	if got := bundled.GeoIPSource(); got != "dbip" {
		t.Fatalf("bundled GeoIP source = %q, want dbip", got)
	}

	cityPath := writeMMDB(t, "GeoLite2-City", mmdbtype.Map{
		"country": mmdbtype.Map{"iso_code": mmdbtype.String("US")},
	})
	maxMind, err := enrich.New(cityPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = maxMind.Close() })
	if got := maxMind.GeoIPSource(); got != "maxmind" {
		t.Fatalf("file GeoIP source = %q, want maxmind", got)
	}
}

func TestNewWithGeoRejectsWrongEdition(t *testing.T) {
	tests := []struct {
		name    string
		edition string
		paths   func(string) enrich.GeoPaths
		want    string
	}{
		{
			name:    "country",
			edition: "GeoLite2-ASN",
			paths:   func(path string) enrich.GeoPaths { return enrich.GeoPaths{Country: path} },
			want:    "country database",
		},
		{
			name:    "city",
			edition: "GeoLite2-Country",
			paths:   func(path string) enrich.GeoPaths { return enrich.GeoPaths{City: path} },
			want:    "city database",
		},
		{
			name:    "ASN",
			edition: "GeoLite2-City",
			paths:   func(path string) enrich.GeoPaths { return enrich.GeoPaths{ASN: path} },
			want:    "ASN database",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeMMDB(t, tt.edition, mmdbtype.Map{
				"country": mmdbtype.Map{"iso_code": mmdbtype.String("US")},
			})
			_, err := enrich.NewWithGeo(tt.paths(path))
			if err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), tt.edition) {
				t.Fatalf("NewWithGeo() error = %v, want %q and edition", err, tt.want)
			}
		})
	}
}

func TestNewWithGeoRejectsCorruptDatabase(t *testing.T) {
	path := writeMMDB(t, "GeoLite2-ASN", mmdbtype.Map{
		"autonomous_system_number": mmdbtype.Uint32(64500),
	})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := maxminddb.OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	separator := int(reader.Metadata.NodeCount * (reader.Metadata.RecordSize / 4))
	_ = reader.Close()
	data[separator] = 1
	corruptPath := filepath.Join(t.TempDir(), "corrupt.mmdb")
	if err := os.WriteFile(corruptPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = enrich.NewWithGeo(enrich.GeoPaths{ASN: corruptPath})
	if err == nil || !strings.Contains(err.Error(), "verify ASN database") {
		t.Fatalf("NewWithGeo() error = %v, want verification failure", err)
	}
}

func TestEnricherSwapGeoPublishesOnlyValidSets(t *testing.T) {
	first := writeMMDB(t, "GeoLite2-Country", mmdbtype.Map{
		"country": mmdbtype.Map{"iso_code": mmdbtype.String("US")},
	})
	second := writeMMDB(t, "GeoLite2-Country", mmdbtype.Map{
		"country": mmdbtype.Map{"iso_code": mmdbtype.String("GB")},
	})
	wrong := writeMMDB(t, "GeoLite2-ASN", mmdbtype.Map{
		"autonomous_system_number": mmdbtype.Uint32(64500),
	})
	e, err := enrich.NewWithGeo(enrich.GeoPaths{Country: first})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	ip := netip.MustParseAddr("192.0.2.42")

	if err := e.SwapGeo(enrich.GeoPaths{Country: wrong}); err == nil {
		t.Fatal("SwapGeo() accepted wrong edition")
	}
	if got := e.Country(ip); got != "US" {
		t.Errorf("Country() after rejected swap = %q, want US", got)
	}
	if err := e.SwapGeo(enrich.GeoPaths{Country: second}); err != nil {
		t.Fatal(err)
	}
	if got := e.Country(ip); got != "GB" {
		t.Errorf("Country() after swap = %q, want GB", got)
	}
}

func TestEnricherSwapGeoConcurrentLookups(t *testing.T) {
	paths := []string{
		writeMMDB(t, "GeoLite2-Country", mmdbtype.Map{
			"country": mmdbtype.Map{"iso_code": mmdbtype.String("US")},
		}),
		writeMMDB(t, "GeoLite2-Country", mmdbtype.Map{
			"country": mmdbtype.Map{"iso_code": mmdbtype.String("GB")},
		}),
	}
	e, err := enrich.NewWithGeo(enrich.GeoPaths{Country: paths[0]})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	ip := netip.MustParseAddr("192.0.2.42")

	start := make(chan struct{})
	errs := make(chan string, 8)
	var lookups sync.WaitGroup
	for range 8 {
		lookups.Go(func() {
			<-start
			for range 2_000 {
				country := e.Country(ip)
				if country != "US" && country != "GB" {
					errs <- country
					return
				}
			}
		})
	}
	close(start)
	for i := range 40 {
		if err := e.SwapGeo(enrich.GeoPaths{Country: paths[i%2]}); err != nil {
			t.Fatal(err)
		}
	}
	lookups.Wait()
	close(errs)
	for country := range errs {
		t.Errorf("Country() during swap = %q, want US or GB", country)
	}
}

func TestEnricherCloseGuardsLifecycle(t *testing.T) {
	path := writeMMDB(t, "GeoLite2-Country", mmdbtype.Map{
		"country": mmdbtype.Map{"iso_code": mmdbtype.String("US")},
	})
	e, err := enrich.NewWithGeo(enrich.GeoPaths{Country: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
	if got := e.Country(netip.MustParseAddr("192.0.2.42")); got != "" {
		t.Errorf("Country() after Close = %q, want empty", got)
	}
	if err := e.SwapGeo(enrich.GeoPaths{Country: path}); err == nil {
		t.Fatal("SwapGeo() after Close succeeded")
	}
}

func writeMMDB(t *testing.T, edition string, record mmdbtype.Map) string {
	t.Helper()
	tree, err := mmdbwriter.New(mmdbwriter.Options{
		BuildEpoch:              1_700_000_000,
		DatabaseType:            edition,
		Description:             map[string]string{"en": "Panel test database"},
		IncludeReservedNetworks: true,
		IPVersion:               4,
		Languages:               []string{"en"},
		RecordSize:              24,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, network, err := net.ParseCIDR(testPrefix.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := tree.Insert(network, record); err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	if _, err := tree.WriteTo(&data); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), edition+".mmdb")
	if err := os.WriteFile(path, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckFileRejectsCorruptSeparator(t *testing.T) {
	path := writeMMDB(t, "GeoLite2-ASN", mmdbtype.Map{
		"autonomous_system_number": mmdbtype.Uint32(64500),
	})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := maxminddb.OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	separator := int(reader.Metadata.NodeCount * (reader.Metadata.RecordSize / 4))
	_ = reader.Close()

	if edition, err := enrich.CheckFile(path); err != nil || edition != "GeoLite2-ASN" {
		t.Fatalf("CheckFile(valid) = %q, %v; want GeoLite2-ASN, nil", edition, err)
	}
	data[separator+3] = 7
	corruptPath := filepath.Join(t.TempDir(), "corrupt.mmdb")
	if err := os.WriteFile(corruptPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := enrich.CheckFile(corruptPath); err == nil {
		t.Fatal("CheckFile(corrupt separator) = nil error, want failure")
	}
	if _, err := enrich.CheckFile(filepath.Join(t.TempDir(), "missing.mmdb")); err == nil {
		t.Fatal("CheckFile(missing) = nil error, want failure")
	}
}
