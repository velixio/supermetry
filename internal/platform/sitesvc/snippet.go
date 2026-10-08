package sitesvc

import (
	"html"
	"path"
	"strings"
)

func Snippet(baseURL, trackerPath, eventPath, publicID string, full bool) string {
	_ = eventPath
	_ = full
	source := strings.TrimRight(baseURL, "/") + siteTrackerPath(trackerPath, publicID)
	return `<script defer src="` + html.EscapeString(source) + `"></script>`
}

func siteTrackerPath(trackerPath, publicID string) string {
	dir := path.Dir("/" + strings.TrimLeft(trackerPath, "/"))
	return dir + "/" + publicID + ".js"
}
