package sitesvc

import (
	"html"
	"strings"
)

func Snippet(baseURL, trackerPath, eventPath, publicID string) string {
	source := strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(trackerPath, "/")
	endpoint := strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(eventPath, "/")
	return `<script defer data-site="` + html.EscapeString(publicID) + `" data-api="` + html.EscapeString(endpoint) + `" src="` + html.EscapeString(source) + `"></script>`
}
