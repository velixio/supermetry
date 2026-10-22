package main

import (
	"testing"
	"time"

	"supermetry/internal/config"
)

func TestNewStarTrackingIsOffWithoutRepo(t *testing.T) {
	got, err := newStarTracking(config.Config{GitHubWebhookSecret: "s", GitHubStarsTZ: "UTC"}, nil, time.Now)
	if err != nil || got.store != nil || got.webhook != nil || got.reconciler != nil {
		t.Fatalf("tracking = %+v, %v", got, err)
	}
}

func TestNewStarTrackingWebhookNeedsSecret(t *testing.T) {
	cfg := config.Config{GitHubStarsRepo: "velixio/supermetry", GitHubStarsTZ: "Asia/Kathmandu"}
	got, err := newStarTracking(cfg, nil, time.Now)
	if err != nil || got.store == nil || got.reconciler == nil || got.webhook != nil {
		t.Fatalf("without secret = %+v, %v", got, err)
	}
	cfg.GitHubWebhookSecret = "s"
	got, err = newStarTracking(cfg, nil, time.Now)
	if err != nil || got.webhook == nil {
		t.Fatalf("with secret = %+v, %v", got, err)
	}
	cfg.GitHubStarsTZ = "Mars/Base"
	if _, err := newStarTracking(cfg, nil, time.Now); err == nil {
		t.Fatal("bad time zone should fail")
	}
}
