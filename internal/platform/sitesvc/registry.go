package sitesvc

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"supermetry/internal/platform/store"
	coresites "supermetry/internal/sites"
)

type registrySnapshot struct {
	byPublic map[string]coresites.Site
	byID     map[int32]coresites.Site
}

// Registry is a periodically refreshed, DB-backed implementation of
// sites.Registry. Each published snapshot is immutable.
type Registry struct {
	store  *store.Store
	value  atomic.Pointer[registrySnapshot]
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func NewRegistry(data *store.Store, refresh time.Duration) *Registry {
	ctx, cancel := context.WithCancel(context.Background())
	registry := &Registry{store: data, cancel: cancel, done: make(chan struct{})}
	registry.value.Store(&registrySnapshot{byPublic: map[string]coresites.Site{}, byID: map[int32]coresites.Site{}})
	_ = registry.refresh(ctx)
	go registry.run(ctx, refresh)
	return registry
}

func (r *Registry) ByPublicID(id string) (coresites.Site, bool) {
	site, ok := r.value.Load().byPublic[id]
	return cloneSite(site), ok
}

func (r *Registry) Site(id int32) (coresites.Site, bool) {
	site, ok := r.value.Load().byID[id]
	return cloneSite(site), ok
}

func (r *Registry) All() []coresites.Site {
	snapshot := r.value.Load()
	sites := make([]coresites.Site, 0, len(snapshot.byID))
	for _, site := range snapshot.byID {
		sites = append(sites, cloneSite(site))
	}
	return sites
}

// Invalidate immediately replaces the current snapshot from PostgreSQL.
func (r *Registry) Invalidate() error {
	return r.refresh(context.Background())
}

func (r *Registry) Close() {
	r.once.Do(func() {
		r.cancel()
		<-r.done
	})
}

func (r *Registry) run(ctx context.Context, interval time.Duration) {
	defer close(r.done)
	if interval <= 0 {
		<-ctx.Done()
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = r.refresh(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (r *Registry) refresh(ctx context.Context) error {
	stored, err := store.ActiveSites(ctx, r.store)
	if err != nil {
		return err
	}
	replayBySite, err := r.loadReplaySettings(ctx)
	if err != nil {
		return err
	}
	next := &registrySnapshot{
		byPublic: make(map[string]coresites.Site, len(stored)),
		byID:     make(map[int32]coresites.Site, len(stored)),
	}
	for _, row := range stored {
		site, err := coreSite(row)
		if err != nil {
			return err
		}
		site.Replay = replayBySite[site.ID]
		next.byPublic[site.PublicID] = site
		next.byID[site.ID] = site
	}
	r.value.Store(next)
	return nil
}

func (r *Registry) loadReplaySettings(ctx context.Context) (map[int32]coresites.ReplaySiteInput, error) {
	rows, err := r.store.Pool.Query(ctx, `
		select s.id, coalesce(s.settings->'replay', '{}'::jsonb)
		from sites s
		join orgs o on o.id = s.org_id
		where s.deleted_at is null and o.deleted_at is null`)
	if err != nil {
		return nil, fmt.Errorf("query replay site settings: %w", err)
	}
	defer rows.Close()
	result := make(map[int32]coresites.ReplaySiteInput)
	for rows.Next() {
		var id int32
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, fmt.Errorf("scan replay site settings: %w", err)
		}
		settings, err := coresites.ParseReplaySiteInput(raw)
		if err != nil {
			return nil, fmt.Errorf("parse replay settings for site %d: %w", id, err)
		}
		result[id] = settings
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate replay site settings: %w", err)
	}
	return result, nil
}

func cloneSite(site coresites.Site) coresites.Site {
	site.AllowedHosts = append([]string(nil), site.AllowedHosts...)
	site.BlockedReferrers = append([]string(nil), site.BlockedReferrers...)
	site.Exclusions = site.Exclusions.Clone()
	site.Tracker = cloneTrackerSettings(site.Tracker)
	site.Replay = cloneReplaySettings(site.Replay)
	return site
}

func cloneReplaySettings(settings coresites.ReplaySiteInput) coresites.ReplaySiteInput {
	cloneBool := func(value *bool) *bool {
		if value == nil {
			return nil
		}
		copied := *value
		return &copied
	}
	cloneInt := func(value *int) *int {
		if value == nil {
			return nil
		}
		copied := *value
		return &copied
	}
	cloneInt64 := func(value *int64) *int64 {
		if value == nil {
			return nil
		}
		copied := *value
		return &copied
	}
	cloneString := func(value *string) *string {
		if value == nil {
			return nil
		}
		copied := *value
		return &copied
	}
	settings.Mask = cloneBool(settings.Mask)
	settings.SampleRate = cloneInt(settings.SampleRate)
	settings.RetentionDays = cloneInt(settings.RetentionDays)
	settings.QuotaBytes = cloneInt64(settings.QuotaBytes)
	settings.Consent = cloneString(settings.Consent)
	settings.Triggers.Error = cloneBool(settings.Triggers.Error)
	settings.Triggers.RageClick = cloneBool(settings.Triggers.RageClick)
	settings.Triggers.URL = cloneBool(settings.Triggers.URL)
	settings.Triggers.Manual = cloneBool(settings.Triggers.Manual)
	settings.Triggers.URLPatterns = append([]string(nil), settings.Triggers.URLPatterns...)
	return settings
}

func cloneTrackerSettings(settings coresites.TrackerSettingsInput) coresites.TrackerSettingsInput {
	clone := func(value *bool) *bool {
		if value == nil {
			return nil
		}
		copied := *value
		return &copied
	}
	return coresites.TrackerSettingsInput{
		Outbound: clone(settings.Outbound), Downloads: clone(settings.Downloads), Forms: clone(settings.Forms),
		Vitals: clone(settings.Vitals), NotFound: clone(settings.NotFound), Hash: clone(settings.Hash), Clicks: clone(settings.Clicks),
	}
}

var _ coresites.Registry = (*Registry)(nil)
