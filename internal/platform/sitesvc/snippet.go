package sitesvc

import (
	"html"
	"strings"
)

func Snippet(baseURL, trackerPath, eventPath, publicID string, full, heatmaps bool) string {
	if full {
		trackerPath = strings.TrimSuffix(trackerPath, ".js") + ".full.js"
	}
	source := strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(trackerPath, "/")
	endpoint := strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(eventPath, "/")
	heatmap := ""
	if full && heatmaps {
		heatmap = ` data-heatmap="true"`
	}
	return `<script defer data-site="` + html.EscapeString(publicID) + `" data-api="` + html.EscapeString(endpoint) + `"` + heatmap + ` src="` + html.EscapeString(source) + `"></script>`
}
