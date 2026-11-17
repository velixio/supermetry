package sentryingest

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

type fixtureItem struct {
	Type    string
	Header  map[string]any
	Payload []byte
}

type fixture struct {
	Name   string
	Header map[string]any
	Items  []fixtureItem
	Meta   map[string]any
}

// loadFixtures reads every testdata envelope with a small test-only parser.
// Brotli bodies are skipped: the decoder for them lives in the front door.
func loadFixtures(t testing.TB) []fixture {
	t.Helper()
	paths := fixtureFiles(t)
	var out []fixture
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) > 2 && raw[0] == 0x1f && raw[1] == 0x8b {
			zr, err := gzip.NewReader(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			if raw, err = io.ReadAll(zr); err != nil {
				t.Fatal(err)
			}
		} else if filepath.Base(p) == "unhandled-exception-br.envelope" {
			continue
		}
		f := fixture{Name: filepath.Base(filepath.Dir(p)) + "/" + filepath.Base(p[:len(p)-len(".envelope")])}
		if side, err := os.ReadFile(p[:len(p)-len(".envelope")] + ".json"); err == nil {
			_ = json.Unmarshal(side, &f.Meta)
		}
		rest := raw
		line := func() []byte {
			i := bytes.IndexByte(rest, '\n')
			var l []byte
			if i < 0 {
				l, rest = rest, nil
			} else {
				l, rest = rest[:i], rest[i+1:]
			}
			return l
		}
		_ = json.Unmarshal(line(), &f.Header)
		for len(rest) > 0 {
			var h map[string]any
			if err := json.Unmarshal(line(), &h); err != nil {
				break
			}
			it := fixtureItem{Header: h}
			it.Type, _ = h["type"].(string)
			if n, ok := h["length"].(float64); ok {
				l := int(n)
				if l > len(rest) {
					l = len(rest)
				}
				it.Payload, rest = rest[:l], rest[l:]
				if len(rest) > 0 && rest[0] == '\n' {
					rest = rest[1:]
				}
			} else {
				it.Payload = line()
			}
			f.Items = append(f.Items, it)
		}
		out = append(out, f)
	}
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }
