package sentryapi

import (
	"bytes"
	"context"
	"io"
	"os"
	"slices"
	"testing"

	"supermetry/internal/breakpad"
	"supermetry/internal/symcache"
)

// A Breakpad .sym uploaded through difs/assemble becomes a breakpad debug file
// whose cache answers symbol lookups and carries call-frame rules.
func TestDifsBreakpadSym(t *testing.T) {
	sym, err := os.ReadFile("../breakpad/testdata/app.sym")
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, func(d *Deps) {
		d.Builders = map[string]CacheBuilder{"breakpad": {Version: 1, Build: breakpad.BuildFunc(t.TempDir())}}
	})
	token := e.svc(t, e.owner, []string{Scope}, nil)
	sum, req := e.difReq(sym, "app.sym")
	e.difs(t, token, "appsite00010", map[string]map[string]any{sum: req})
	if e.upload(t, token, "file", sum, sym) != 200 {
		t.Fatal("upload")
	}
	_, _ = e.difs(t, token, "appsite00010", map[string]map[string]any{sum: req})
	final := e.waitDIF(t, token, "appsite00010", sum, req)
	if final["state"] != "ok" {
		t.Fatalf("final = %v", final)
	}

	var kind, state, key, debugID string
	var features []string
	if err := e.h.deps.Pool.QueryRow(context.Background(),
		`select kind, cache_state, cache_key, debug_id, features from app_debug_files where upload_id=(select id from app_dif_uploads where checksum=$1) and site_id=10`, sum).
		Scan(&kind, &state, &key, &debugID, &features); err != nil {
		t.Fatal(err)
	}
	if kind != "breakpad" || state != "ok" || debugID != "7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01" || !slices.Equal(features, []string{"symtab", "debug", "unwind"}) {
		t.Fatalf("row = %s %s %s %v", kind, state, debugID, features)
	}
	rc, err := e.blobs.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	c, err := symcache.Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := c.Lookup(0x24f); !ok || got[0].Function != "main" || got[0].File != "app.c" || got[0].Line != 31 {
		t.Fatalf("lookup = %v %v", got, ok)
	}
	tab, err := breakpad.OpenCFI(c)
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := tab.RulesAt(0x210); r != ".cfa: $rsp 16 + .ra: .cfa -8 + ^ $rbp: .cfa -16 + ^" {
		t.Fatalf("rules = %q", r)
	}
}
