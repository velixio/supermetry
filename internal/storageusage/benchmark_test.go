package storageusage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

// BenchmarkPublishThousandSites measures the staging and atomic-publication
// cost independently of relation and blob scans.
func BenchmarkPublishThousandSites(b *testing.B) {
	pool, _ := testpg.New(b)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		b.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values('Storage benchmark','storage-benchmark') returning id`).Scan(&orgID); err != nil {
		b.Fatal(err)
	}
	siteIDs := make([]int32, 0, 1_000)
	for i := 0; i < 1_000; i++ {
		var siteID int32
		if err := pool.QueryRow(ctx, `
			insert into sites(org_id,public_id,name,timezone,retention_class)
			values($1,$2,$2,'UTC',30) returning id`, orgID, fmt.Sprintf("bench%07d", i)).Scan(&siteID); err != nil {
			b.Fatal(err)
		}
		siteIDs = append(siteIDs, siteID)
	}
	contributions := make([]Contribution, len(siteIDs))
	for i, siteID := range siteIDs {
		contributions[i] = Contribution{SiteID: siteID, Bytes: int64(i + 1)}
	}
	store := NewStore(pool)
	started := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now := started.Add(time.Duration(i) * time.Second)
		run, err := store.BeginOrResume(ctx, MeasurementVersion, now)
		if err != nil {
			b.Fatal(err)
		}
		if err := store.ReplaceSource(ctx, run.ID, "benchmark", CategoryHot, contributions); err != nil {
			b.Fatal(err)
		}
		if err := store.Publish(ctx, run.ID, now); err != nil {
			b.Fatal(err)
		}
	}
}
