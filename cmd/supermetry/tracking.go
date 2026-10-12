package main

import (
	"log/slog"

	"supermetry/internal/config"
	"supermetry/internal/privacy"
)

// logTrackingMode reports the instance's tracking mode at startup. Identified is the default, so
// say so when it was not chosen, and how to get the strictest mode.
func logTrackingMode(logger *slog.Logger, mode config.TrackingMode) {
	if mode.Explicit {
		logger.Info("tracking mode", "mode", mode.Mode.String(), "source", config.TrackingModeEnv)
		return
	}
	if mode.Mode == privacy.Identified {
		logger.Info("tracking mode is identified by default: page paths, referrers and campaigns are collected, with no cookies and no raw IP addresses; set "+config.TrackingModeEnv+"=anonymous for the strictest mode", "mode", "identified")
	}
}
