package db

import (
	"strings"
	"testing"
)

func TestStorageUsageMigrationIsLatestAndDefinesAtomicGenerationTables(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if got := migrations[len(migrations)-1].version; got != 245 {
		t.Fatalf("latest migration = %d, want 245", got)
	}
	sql := migrations[len(migrations)-1].sql
	for _, fragment := range []string{
		"storage_usage_runs", "storage_usage_run_sites", "storage_usage_run_work", "storage_usage_stage", "site_storage_usage",
		"measurement_version", "work_fingerprint", "replay_recording_bytes", "estimated_storage_bytes",
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("migration is missing %q", fragment)
		}
	}
}
