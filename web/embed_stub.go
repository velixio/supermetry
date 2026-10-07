//go:build !embedui

package web

import (
	"io/fs"
	"testing/fstest"
)

type stubFiles struct{}

func (stubFiles) files() fs.FS {
	return fstest.MapFS{
		"index.html": {
			Data: []byte("<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>Panel</title></head><body><main><p>UI not built — run <code>make web</code>.</p></main></body></html>"),
		},
	}
}

var builtUI uiFileSystem = stubFiles{}
