package sentryapi

import (
	"context"
	"io"
	"strings"
	"testing"

	"supermetry/internal/debugfile"
)

// A file reaching cache_state ok clears the missing-symbols row of the
// projects the upload targets for its debug id, and nothing else.
func TestDifsReadyClearsMissingRows(t *testing.T) {
	build := func(_ context.Context, _ debugfile.Object, _ io.ReaderAt, _ int64, out io.Writer) ([]string, error) {
		_, err := out.Write([]byte("v"))
		return nil, err
	}
	e := newEnv(t, func(d *Deps) { d.Builders = map[string]CacheBuilder{"proguard": {Version: 1, Build: build}} })
	token := e.svc(t, e.owner, []string{Scope}, nil)
	ctx := context.Background()
	if _, err := e.h.deps.Pool.Exec(ctx, `insert into app_missing_debug_files(site_id, kind, debug_id, first_seen, last_seen, events) values
		(10,'proguard',$1,now(),now(),3),(10,'proguard','11111111-1111-1111-1111-111111111111',now(),now(),1),(20,'proguard',$1,now(),now(),2)`, proguardID); err != nil {
		t.Fatal(err)
	}
	sum, req := e.difReq(proguardFile, proguardName)
	e.difs(t, token, "appsite00010", map[string]map[string]any{sum: req})
	e.upload(t, token, "file", sum, proguardFile)
	if got := e.waitDIF(t, token, "appsite00010", sum, req); got["state"] != "ok" {
		t.Fatal(got)
	}
	var left []string
	rows, err := e.h.deps.Pool.Query(ctx, `select site_id::text || ':' || debug_id from app_missing_debug_files order by 1`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		left = append(left, s)
	}
	rows.Close()
	// Project 20 is another project: the upload targeted project 10 only.
	want := "10:11111111-1111-1111-1111-111111111111,20:" + proguardID
	if strings.Join(left, ",") != want {
		t.Fatalf("left %v, want %s", left, want)
	}
}
