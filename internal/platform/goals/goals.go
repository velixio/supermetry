// Package goals provides cached goal and funnel definitions to the query layer.
package goals

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"panel/internal/platform/store"
)

type Goal struct {
	ID              int64
	SiteID          int32
	Kind            string
	Match           string
	Value           string
	PropKey         string
	PropValue       string
	Name            string
	Currency        string
	ScrollThreshold int16
}

type Step struct {
	Kind      string `json:"kind"`
	Match     string `json:"match"`
	Value     string `json:"value"`
	PropKey   string `json:"prop_key,omitempty"`
	PropValue string `json:"prop_value,omitempty"`
}

type Funnel struct {
	ID     int64
	SiteID int32
	Name   string
	Steps  []Step
}

type snapshot struct {
	goals   map[int32][]Goal
	funnels map[int32]map[int64]Funnel
}

type Goals struct {
	store  *store.Store
	value  atomic.Pointer[snapshot]
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func New(data *store.Store, refresh time.Duration) *Goals {
	ctx, cancel := context.WithCancel(context.Background())
	goals := &Goals{store: data, cancel: cancel, done: make(chan struct{})}
	goals.value.Store(&snapshot{goals: map[int32][]Goal{}, funnels: map[int32]map[int64]Funnel{}})
	_ = goals.refresh(ctx)
	go goals.run(ctx, refresh)
	return goals
}

func (g *Goals) Goals(siteID int32) []Goal {
	return append([]Goal(nil), g.value.Load().goals[siteID]...)
}

func (g *Goals) Funnel(siteID int32, id int64) (Funnel, bool) {
	funnel, ok := g.value.Load().funnels[siteID][id]
	if !ok {
		return Funnel{}, false
	}
	funnel.Steps = append([]Step(nil), funnel.Steps...)
	return funnel, true
}

func (g *Goals) Invalidate() error {
	return g.refresh(context.Background())
}

func (g *Goals) Close() {
	g.once.Do(func() {
		g.cancel()
		<-g.done
	})
}

func (g *Goals) run(ctx context.Context, interval time.Duration) {
	defer close(g.done)
	if interval <= 0 {
		<-ctx.Done()
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = g.refresh(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (g *Goals) refresh(ctx context.Context) error {
	storedGoals, err := store.ActiveGoals(ctx, g.store)
	if err != nil {
		return err
	}
	storedFunnels, err := store.ActiveFunnels(ctx, g.store)
	if err != nil {
		return err
	}
	next := &snapshot{
		goals:   make(map[int32][]Goal),
		funnels: make(map[int32]map[int64]Funnel),
	}
	for _, goal := range storedGoals {
		next.goals[goal.SiteID] = append(next.goals[goal.SiteID], Goal{
			ID: goal.ID, SiteID: goal.SiteID, Kind: goal.Kind, Match: goal.Match,
			Value: goal.Value, PropKey: goal.PropKey, PropValue: goal.PropValue, Name: goal.Name,
			Currency: goal.Currency, ScrollThreshold: goal.ScrollThreshold,
		})
	}
	for _, funnel := range storedFunnels {
		var steps []Step
		if err := json.Unmarshal(funnel.Steps, &steps); err != nil {
			return fmt.Errorf("decode funnel %d steps: %w", funnel.ID, err)
		}
		if next.funnels[funnel.SiteID] == nil {
			next.funnels[funnel.SiteID] = make(map[int64]Funnel)
		}
		next.funnels[funnel.SiteID][funnel.ID] = Funnel{
			ID: funnel.ID, SiteID: funnel.SiteID, Name: funnel.Name, Steps: steps,
		}
	}
	g.value.Store(next)
	return nil
}
