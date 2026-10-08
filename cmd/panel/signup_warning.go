package main

import (
	"context"
	"log/slog"
)

func warnPublicSignup(ctx context.Context, logger *slog.Logger, mode string) {
	if mode == "open" {
		logger.Log(ctx, slog.LevelWarn, "public signup is open")
	}
}
