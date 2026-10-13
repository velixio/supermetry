//go:build embedui

package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var embeddedFiles embed.FS

type releaseFiles struct{}

func (releaseFiles) files() fs.FS {
	dist, err := fs.Sub(embeddedFiles, "dist")
	if err != nil {
		panic("web: embedded dist directory is missing: " + err.Error())
	}
	return dist
}

var builtUI uiFileSystem = releaseFiles{}

// wrapUI is a no-op: the embedded UI is always served as is.
func wrapUI(h http.Handler) http.Handler { return h }
