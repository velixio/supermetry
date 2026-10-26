package storageusage

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/replay"
)

type ReplayScanner struct {
	pool  *pgxpool.Pool
	blobs replay.BlobStore
}

func NewReplayScanner(pool *pgxpool.Pool, blobs replay.BlobStore) *ReplayScanner {
	return &ReplayScanner{pool: pool, blobs: blobs}
}

func (s *ReplayScanner) Fingerprint() string { return "replay-blob-estimator-v1" }

func (s *ReplayScanner) WorkUnits(ctx context.Context, runID int64) ([]WorkUnit, error) {
	rows, err := s.pool.Query(ctx, `select rs.site_id from storage_usage_run_sites rs join sites s on s.id=rs.site_id and s.erased_at is null where rs.run_id=$1 order by rs.site_id`, runID)
	if err != nil {
		return nil, fmt.Errorf("list replay storage sites: %w", err)
	}
	defer rows.Close()
	var units []WorkUnit
	for rows.Next() {
		var siteID int32
		if err := rows.Scan(&siteID); err != nil {
			return nil, fmt.Errorf("scan replay storage site: %w", err)
		}
		units = append(units, replayWorkUnit(siteID))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate replay storage sites: %w", err)
	}
	return units, nil
}

func (s *ReplayScanner) Scan(ctx context.Context, runID int64, unit WorkUnit) ([]Measurement, error) {
	expected := replayWorkUnit(unit.SiteID)
	expected.Scanner = unit.Scanner
	if unit != expected {
		return nil, fmt.Errorf("invalid replay storage work unit %q", unit.Key)
	}
	captured, err := s.siteCaptured(ctx, runID, unit.SiteID)
	if err != nil {
		return nil, err
	}
	if !captured {
		return nil, nil
	}

	prefix := siteReplayPrefix(unit.SiteID)
	var recordingBytes, cssBytes int64
	add := func(blob replay.BlobInfo) error {
		if blob.Size < 0 {
			return fmt.Errorf("replay blob %q has negative size", blob.Key)
		}
		if !strings.HasPrefix(blob.Key, prefix+"/") {
			return nil
		}
		isCSS := strings.HasPrefix(blob.Key, prefix+"/css/")
		total := &recordingBytes
		if isCSS {
			total = &cssBytes
		}
		if blob.Size > math.MaxInt64-*total {
			return fmt.Errorf("replay storage size overflows bigint for site %d", unit.SiteID)
		}
		*total += blob.Size
		return nil
	}
	if walker, ok := s.blobs.(replay.BlobWalker); ok {
		if err := walker.Walk(ctx, prefix, add); err != nil {
			return nil, fmt.Errorf("walk replay storage for site %d: %w", unit.SiteID, err)
		}
	} else {
		blobs, err := s.blobs.List(ctx, prefix)
		if err != nil {
			return nil, fmt.Errorf("list replay storage for site %d: %w", unit.SiteID, err)
		}
		seen := make(map[string]int64, len(blobs))
		for _, blob := range blobs {
			if previous, duplicate := seen[blob.Key]; duplicate {
				if previous != blob.Size {
					return nil, fmt.Errorf("replay blob %q has conflicting sizes", blob.Key)
				}
				continue
			}
			seen[blob.Key] = blob.Size
			if err := add(blob); err != nil {
				return nil, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Erasure may complete while the external listing is in progress. Recheck
	// before returning a contribution that the reconciler could stage.
	captured, err = s.siteCaptured(ctx, runID, unit.SiteID)
	if err != nil {
		return nil, err
	}
	if !captured {
		return nil, nil
	}
	return []Measurement{
		{Category: CategoryReplayRecording, Values: []Contribution{{SiteID: unit.SiteID, Bytes: recordingBytes}}},
		{Category: CategoryReplayCSS, Values: []Contribution{{SiteID: unit.SiteID, Bytes: cssBytes}}},
	}, nil
}

func (s *ReplayScanner) siteCaptured(ctx context.Context, runID int64, siteID int32) (bool, error) {
	var captured bool
	if err := s.pool.QueryRow(ctx, `
		select exists(
			select 1
			from storage_usage_run_sites rs
			join storage_usage_runs r on r.id=rs.run_id and r.status='active'
			where rs.run_id=$1 and rs.site_id=$2
		)`, runID, siteID).Scan(&captured); err != nil {
		return false, fmt.Errorf("validate captured replay site: %w", err)
	}
	return captured, nil
}

func replayWorkUnit(siteID int32) WorkUnit {
	if siteID < 1 {
		return WorkUnit{}
	}
	return WorkUnit{
		Key:    fmt.Sprintf("replay:%010d", siteID),
		SiteID: siteID,
	}
}

func siteReplayPrefix(siteID int32) string {
	return "replay/" + strconv.FormatInt(int64(siteID), 10)
}

var _ Scanner = (*ReplayScanner)(nil)
