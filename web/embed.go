package web

import "io/fs"

type uiFileSystem interface {
	files() fs.FS
}
