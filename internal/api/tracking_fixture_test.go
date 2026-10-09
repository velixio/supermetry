package api_test

import (
	"net/http"
	"panel/internal/api"
	"panel/internal/privacy"
)

func identifiedRouter(deps api.Deps) http.Handler {
	deps.Config.Tracking.Mode = privacy.Identified
	return api.NewRouter(deps)
}
func identifiedMCPHandler(deps api.MCPDeps) http.Handler {
	deps.TrackingMode = privacy.Identified
	return api.NewMCPHandler(deps)
}
