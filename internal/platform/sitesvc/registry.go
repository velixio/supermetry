package sitesvc

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"panel/internal/platform/store"
	coresites "panel/internal/sites"
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
	next := &registrySnapshot{
		byPublic: make(map[string]coresites.Site, len(stored)),
		byID:     make(map[int32]coresites.Site, len(stored)),
	}
	for _, row := range stored {
		site, err := coreSite(row)
		if err != nil {
			return err
		}
		next.byPublic[site.PublicID] = site
		next.byID[site.ID] = site
	}
	r.value.Store(next)
	return nil
}

func cloneSite(site coresites.Site) coresites.Site {
	site.AllowedHosts = append([]string(nil), site.AllowedHosts...)
	site.Exclusions = site.Exclusions.Clone()
	return site
}

var _ coresites.Registry = (*Registry)(nil)
