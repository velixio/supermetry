package main

import (
	"strings"
	"testing"
)

func TestLoadRequestSets(t *testing.T) {
	t.Parallel()
	inventory := []InventoryRoute{
		{Method: "GET", Pattern: "/api/public/v1/stats/{site}"},
		{Method: "POST", Pattern: "/api/private/goals"},
	}
	tests := []struct {
		name    string
		yaml    string
		wantLen int
		wantErr string
	}{
		{
			name: "defaults only covers registered routes",
			yaml: `defaults:
  params:
    period: 7d
routes: []
`,
			wantLen: 2,
		},
		{
			name: "unknown route rejected",
			yaml: `routes:
  - method: GET
    route: /api/public/v1/missing
`,
			wantErr: "unknown route",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := loadRequestSets(strings.NewReader(tt.yaml), inventory)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("loadRequestSets() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadRequestSets() error = %v", err)
			}
			if len(got) != tt.wantLen {
				t.Fatalf("len(loadRequestSets()) = %d, want %d", len(got), tt.wantLen)
			}
		})
	}
}
