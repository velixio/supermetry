//go:build !embedui

package web

import (
	"io/fs"
	"net/http"
	"os"
	"testing/fstest"
)

type stubFiles struct{}

func (stubFiles) files() fs.FS {
	return fstest.MapFS{"index.html": {Data: stubIndex(os.Getenv(DevUIEnv))}}
}

var builtUI uiFileSystem = stubFiles{}

// wrapUI redirects to the Vite dev server when SUPERMETRY_DEV_UI_URL is set.
func wrapUI(h http.Handler) http.Handler { return devUI(h, os.Getenv(DevUIEnv)) }
