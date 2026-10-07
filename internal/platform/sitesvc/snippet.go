package sitesvc

import (
	"html"
	"strings"
)

func Snippet(baseURL, trackerPath, publicID string) string {
	source := strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(trackerPath, "/")
	return `<script defer data-site="` + html.EscapeString(publicID) + `" src="` + html.EscapeString(source) + `"></script>`
}
