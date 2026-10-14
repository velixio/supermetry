package main

import "testing"

func TestManagedUpdaterRejectsDevelopmentVersions(t *testing.T) {
	client, mode := managedUpdater("dev")
	if client != nil || mode != "" {
		t.Fatalf("client=%v mode=%q", client, mode)
	}
}
