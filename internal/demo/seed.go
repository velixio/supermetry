package demo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/closer"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/store"
	"supermetry/internal/replay"
	"supermetry/internal/retention"
	"supermetry/internal/sites"
	"supermetry/internal/synth"
)

// Create inserts the organization's sole active demo and starts its seed.
func (s *Service) Create(ctx context.Context, principal authz.Principal, orgID int64, timezone string) (store.Site, error) {
	if err := s.configurationError(); err != nil {
		return store.Site{}, err
	}
	if s.deps.Authz == nil {
		return store.Site{}, errors.New("demo authorization is not configured")
	}
	if err := s.deps.Authz(ctx, principal, orgID); err != nil {
		return store.Site{}, err
	}
	if timezone == "" {
		timezone = "UTC"
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return store.Site{}, fmt.Errorf("%w: %s", ErrInvalidTimezone, timezone)
	}
	now := s.deps.Now()
	localNow := now.In(location)
	createdAt := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location).AddDate(0, 0, -s.deps.Window+1)

	var created store.Site
	err = pgx.BeginFunc(ctx, s.deps.Pool, func(tx pgx.Tx) error {
		var createErr error
		created, createErr = store.CreateDemoSite(ctx, tx, store.Site{
			OrgID: orgID, Name: "Demo project",
			AllowedHosts: []string{"www.demo.example", "docs.demo.example"},
			Timezone:     timezone, RetentionClass: 90,
		}, createdAt)
		if createErr != nil {
			return createErr
		}
		if s.deps.Audit != nil {
			return s.deps.Audit(ctx, tx, principal, orgID, created.ID)
		}
		return nil
	})
	if err != nil {
		return store.Site{}, err
	}
	if err := s.deps.Registry.Invalidate(); err != nil {
		return store.Site{}, fmt.Errorf("refresh site registry: %w", err)
	}
	run, started := s.tryStartSeed(created.ID)
	if !started {
		return store.Site{}, errors.New("demo seed already running")
	}
	s.wg.Add(1)
	go func(siteID int32) {
		defer s.wg.Done()
		run.err = s.seedWithRetry(s.ctx, siteID)
		s.finishSeed(siteID, run)
		if run.err != nil && !errors.Is(run.err, context.Canceled) {
			slog.Error("demo seed failed", "site_id", siteID, "error", run.err)
		}
	}(created.ID)
	return created, nil
}

func (s *Service) seedWithRetry(ctx context.Context, siteID int32) error {
	delays := [...]time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second}
	var seedErr error
	for attempt := 0; attempt <= len(delays); attempt++ {
		seedErr = s.seed(ctx, siteID)
		if seedErr == nil {
			return nil
		}
		slog.Error("demo seed attempt failed", "site_id", siteID, "error", seedErr)
		if err := ctx.Err(); err != nil {
			return err
		}
		if attempt == len(delays) {
			break
		}
		if err := s.waitRetry(ctx, delays[attempt]); err != nil {
			return err
		}
		if err := s.erasePartial(ctx, siteID); err != nil {
			return errors.Join(seedErr, err)
		}
	}
	_, markErr := s.deps.Pool.Exec(ctx, `
		update sites
		set settings=jsonb_set(settings,'{demo_seed_failed_at}',to_jsonb($2::text),true)
		where id=$1 and is_demo and deleted_at is null`, siteID, s.deps.Now().UTC().Format(time.RFC3339Nano))
	return errors.Join(seedErr, markErr)
}

func (s *Service) waitRetry(ctx context.Context, delay time.Duration) error {
	if s.deps.RetryWait != nil {
		return s.deps.RetryWait(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// SeedNow synchronously seeds a demo project.
func (s *Service) SeedNow(ctx context.Context, siteID int32) error {
	if err := s.configurationError(); err != nil {
		return err
	}
	var unseeded bool
	if err := s.deps.Pool.QueryRow(ctx, `
		select demo_seeded_at is null from sites
		where id=$1 and is_demo and deleted_at is null`, siteID).Scan(&unseeded); err != nil {
		return err
	}
	if !unseeded {
		return ErrDemoAlreadySeeded
	}
	return s.seedOnce(ctx, siteID)
}

func (s *Service) seedOnce(ctx context.Context, siteID int32) error {
	s.mu.Lock()
	if current := s.running[siteID]; current != nil {
		s.mu.Unlock()
		select {
		case <-current.done:
			return current.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	run := &seedRun{done: make(chan struct{})}
	s.running[siteID] = run
	s.mu.Unlock()

	run.err = s.seed(ctx, siteID)
	s.mu.Lock()
	delete(s.running, siteID)
	close(run.done)
	s.mu.Unlock()
	return run.err
}

func (s *Service) seed(ctx context.Context, siteID int32) error {
	if s.deps.SeedStep != nil {
		if err := s.deps.SeedStep(ctx, siteID); err != nil {
			return err
		}
	}
	site, err := s.site(siteID)
	if err != nil {
		return err
	}
	now := s.deps.Now()
	location := site.TZ
	if location == nil {
		location = time.UTC
	}
	localNow := now.In(location)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	start := today.AddDate(0, 0, -s.deps.Window+1)
	seed := s.deps.Seed
	if seed == 0 {
		seed = int64(site.ID)
	}
	visits := synth.NewDemo(synth.DemoConfig{
		Seed: seed, TZ: location, Visitors: s.deps.Visitors, Anchor: today,
	}).Visits(start, now)
	if err := s.writeHistory(ctx, site, visits); err != nil {
		return fmt.Errorf("seed demo history: %w", err)
	}
	for day := start; day.Before(today); day = day.AddDate(0, 0, 1) {
		civilDay := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
		if _, err := closer.CloseDay(ctx, s.deps.Pool, []int32{site.ID}, civilDay); err != nil {
			return fmt.Errorf("close demo day %s: %w", civilDay.Format(time.DateOnly), err)
		}
	}
	ownerID, err := s.demoOwner(ctx, site.OrgID)
	if err != nil {
		return err
	}
	if err := s.createExamples(ctx, site, ownerID); err != nil {
		return fmt.Errorf("create demo examples: %w", err)
	}
	if err := store.SetDemoLiveThrough(ctx, s.deps.Pool, site.ID, now); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if _, err := s.deps.Pool.Exec(ctx, `update sites set settings=settings-'demo_seed_failed_at' where id=$1`, site.ID); err != nil {
		return fmt.Errorf("clear demo seed failure: %w", err)
	}
	if err := store.SetDemoSeeded(ctx, s.deps.Pool, site.ID, now); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	return nil
}

func (s *Service) demoOwner(ctx context.Context, orgID int64) (int64, error) {
	var ownerID int64
	err := s.deps.Pool.QueryRow(ctx, `
		select user_id from memberships
		where org_id=$1 and role='owner'
		order by created_at,user_id limit 1`, orgID).Scan(&ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load demo owner: %w", err)
	}
	return ownerID, nil
}

func (s *Service) site(siteID int32) (sites.Site, error) {
	if site, ok := s.deps.Registry.Site(siteID); ok && site.Demo {
		return site, nil
	}
	if err := s.deps.Registry.Invalidate(); err != nil {
		return sites.Site{}, fmt.Errorf("refresh site registry: %w", err)
	}
	if site, ok := s.deps.Registry.Site(siteID); ok && site.Demo {
		return site, nil
	}
	return sites.Site{}, fmt.Errorf("demo site %d not found", siteID)
}

// ResumeUnseeded erases partial demo data and deterministically seeds it again.
func (s *Service) ResumeUnseeded(ctx context.Context) error {
	if s == nil || s.deps.Pool == nil {
		return errors.New("demo service is not configured")
	}
	siteIDs, err := store.UnseededDemoSites(ctx, s.deps.Pool)
	if err != nil {
		return err
	}
	for _, siteID := range siteIDs {
		run, started := s.tryStartSeed(siteID)
		if !started {
			continue
		}
		run.err = s.resumeSite(ctx, siteID)
		s.finishSeed(siteID, run)
		if errors.Is(run.err, ErrDemoAlreadySeeded) {
			continue
		}
		if run.err != nil {
			return run.err
		}
	}
	return nil
}

func (s *Service) resumeSite(ctx context.Context, siteID int32) error {
	var unseeded bool
	if err := s.deps.Pool.QueryRow(ctx, `select demo_seeded_at is null from sites where id=$1 and is_demo and deleted_at is null`, siteID).Scan(&unseeded); err != nil {
		return err
	}
	if !unseeded {
		return ErrDemoAlreadySeeded
	}
	if err := s.erasePartial(ctx, siteID); err != nil {
		return err
	}
	return s.seed(ctx, siteID)
}

func (s *Service) erasePartial(ctx context.Context, siteID int32) error {
	if s.deps.Blobs != nil {
		if err := replay.EraseSite(ctx, s.deps.Pool, s.deps.Blobs, siteID); err != nil {
			return fmt.Errorf("erase demo replay %d: %w", siteID, err)
		}
	} else if _, err := s.deps.Pool.Exec(ctx, `delete from replay_sessions where site_id=$1`, siteID); err != nil {
		return fmt.Errorf("erase demo replay index %d: %w", siteID, err)
	}
	if err := retention.Erase(ctx, s.deps.Pool, siteID, 10_000, 0); err != nil {
		return fmt.Errorf("erase partial demo %d: %w", siteID, err)
	}
	return nil
}

func (s *Service) tryStartSeed(siteID int32) (*seedRun, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[siteID] != nil {
		return nil, false
	}
	run := &seedRun{done: make(chan struct{})}
	s.running[siteID] = run
	return run, true
}

func (s *Service) finishSeed(siteID int32, run *seedRun) {
	s.mu.Lock()
	delete(s.running, siteID)
	close(run.done)
	s.mu.Unlock()
}

func (s *Service) seedRunning(siteID int32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[siteID] != nil
}
