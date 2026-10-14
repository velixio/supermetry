//go:build !embedui

package web

import (
	"io/fs"
	"net/http"
	"os"
	"testing/fstest"
)

const stubIndex = "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>Supermetry</title></head><body><main><p>UI not built — run <code>make web</code>.</p></main></body></html>"

type stubFiles struct{}

func (stubFiles) files() fs.FS {
	return fstest.MapFS{"index.html": {Data: []byte(stubIndex)}}
}

var builtUI uiFileSystem = stubFiles{}

// wrapUI forwards pages to the Vite dev server when SUPERMETRY_DEV_UI_URL is set.
func wrapUI(h http.Handler) http.Handler { return devUI(h, os.Getenv(DevUIEnv)) }
