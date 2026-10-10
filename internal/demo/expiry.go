package demo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/store"
	"supermetry/internal/scheduler"
)

func (s *Service) ExpireDue(ctx context.Context) error {
	if s == nil || s.deps.Pool == nil || s.deps.Registry == nil {
		return errors.New("demo expiry service is not configured")
	}
	s.tickMu.Lock()
	defer s.tickMu.Unlock()

	expired, err := store.ExpiredDemoSites(ctx, s.deps.Pool, s.deps.Now().Add(-DemoLifetime))
	if err != nil {
		return err
	}
	var expiryErrors []error
	deleted := make([]int32, 0, len(expired))
	for _, site := range expired {
		if s.seedRunning(site.SiteID) {
			continue
		}
		err := pgx.BeginFunc(ctx, s.deps.Pool, func(tx pgx.Tx) error {
			if err := store.SoftDeleteSite(ctx, tx, site.OrgID, site.SiteID); err != nil {
				return err
			}
			return audit.Write(ctx, tx, authz.Principal{OrgID: site.OrgID}, "site.deleted", "site", int64(site.SiteID), nil)
		})
		if err != nil {
			expiryErrors = append(expiryErrors, fmt.Errorf("expire demo site %d: %w", site.SiteID, err))
			continue
		}
		deleted = append(deleted, site.SiteID)
		s.dropPending(site.SiteID)
	}
	if len(deleted) > 0 {
		if err := s.deps.Registry.Invalidate(); err != nil {
			expiryErrors = append(expiryErrors, fmt.Errorf("refresh site registry: %w", err))
		}
	}
	return errors.Join(expiryErrors...)
}

func (s *Service) dropPending(siteID int32) {
	delete(s.pending, siteID)
	delete(s.busyPending, siteID)
	delete(s.busyWatching, siteID)
	delete(s.busySeq, siteID)
	delete(s.cursors, siteID)
	s.viewMu.Lock()
	delete(s.viewed, siteID)
	delete(s.watched, siteID)
	s.viewMu.Unlock()
}

func (s *Service) ExpiryJob() scheduler.Job {
	return scheduler.Job{Name: "demo-expiry", Every: time.Minute, Run: s.ExpireDue}
}
