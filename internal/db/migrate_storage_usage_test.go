package db

import (
	"strings"
	"testing"
)

func TestStorageUsageMigrationDefinesAtomicGenerationTables(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	sql := ""
	for _, m := range migrations {
		if m.version == 245 {
			sql = m.sql
		}
	}
	if sql == "" {
		t.Fatal("migration 245 is missing")
	}
	for _, fragment := range []string{
		"storage_usage_runs", "storage_usage_run_sites", "storage_usage_run_work", "storage_usage_stage", "site_storage_usage",
		"measurement_version", "work_fingerprint", "replay_recording_bytes", "estimated_storage_bytes",
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("migration is missing %q", fragment)
		}
	}
}
