package fixtures

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const root = "../../internal/sentryingest/testdata"

type sidecar struct {
	Headers   map[string]string `json:"headers"`
	Synthetic bool              `json:"synthetic"`
}

func plain(t *testing.T, raw []byte, enc string) []byte {
	t.Helper()
	if enc != "gzip" {
		return raw
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("not gzip: %v", err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFixturesAreRawBodiesWithSidecars(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(root, "*", "*.envelope"))
	if len(files) == 0 {
		t.Fatal("no fixtures found")
	}
	for _, f := range files {
		sdk := filepath.Base(filepath.Dir(f))
		raw, _ := os.ReadFile(f)
		sb, err := os.ReadFile(strings.TrimSuffix(f, ".envelope") + ".json")
		if err != nil {
			t.Errorf("%s: missing sidecar", f)
			continue
		}
		var sc sidecar
		if err := json.Unmarshal(sb, &sc); err != nil {
			t.Errorf("%s: bad sidecar: %v", f, err)
			continue
		}
		enc := sc.Headers["Content-Encoding"]
		switch enc {
		case "gzip":
			if len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
				t.Errorf("%s: Content-Encoding gzip but body has no gzip magic (recorded after decompression?)", f)
				continue
			}
		case "br":
			if bytes.HasPrefix(raw, []byte("{")) {
				t.Errorf("%s: Content-Encoding br but body is plain JSON", f)
			}
			continue
		}
		body := plain(t, raw, enc)
		if !bytes.HasPrefix(body, []byte("{")) {
			t.Errorf("%s: envelope does not start with a JSON header line", f)
		}
		// Recorded SDK captures are real; android, cocoa and the transaction
		// fixtures other than python are built by tools/sentry-fixtures/synthetic.
		wantSynthetic := sdk == "android" || sdk == "cocoa" ||
			(sdk == "transactions" && filepath.Base(f) != "python.envelope")
		if wantSynthetic != sc.Synthetic {
			t.Errorf("%s: synthetic flag must be true exactly for android, cocoa and the built transaction fixtures", f)
		}
		if sdk == "python" && !strings.Contains(f, "-br") && enc != "gzip" {
			t.Errorf("%s: python fixtures other than -br must be gzip", f)
		}
	}
}

func TestElectronNativeCrashHasMinidump(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root, "electron", "native-crash.envelope"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(plain(t, raw, "gzip"), []byte(`"attachment_type":"event.minidump"`)) {
		t.Fatal("native-crash fixture lacks the event.minidump attachment")
	}
}
