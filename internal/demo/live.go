package demo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"supermetry/internal/closer"
	"supermetry/internal/enrich"
	"supermetry/internal/platform/store"
	"supermetry/internal/realtime"
	"supermetry/internal/replay"
	"supermetry/internal/salts"
	"supermetry/internal/scheduler"
	"supermetry/internal/session"
	"supermetry/internal/sites"
	"supermetry/internal/synth"
	"supermetry/internal/writer"
)

const (
	demoJobInterval    = 15 * time.Second
	demoActiveWindow   = 3 * time.Hour
	demoGapThreshold   = 30 * time.Minute
	demoLiveTail       = 5 * time.Minute
	demoViewedThrottle = 5 * time.Minute
	heartbeatTTL       = 60 * time.Second
	watchPruneAfter    = 10 * time.Minute
)

type pendingVisit struct {
	visit         synth.DemoVisit
	next          int
	sessionID     int64
	localDay      time.Time
	pathID        int32
	replayWritten bool
}

type liveHit struct {
	visit int
	hit   int
	at    time.Time
}

// Job returns the single-instance scheduler entry for live demo traffic.
func (s *Service) Job() scheduler.Job {
	return scheduler.Job{Name: "demo-live", Every: demoJobInterval, Run: s.Tick}
}

// Viewed records recent dashboard use at most once per five-minute window.
func (s *Service) Viewed(siteID int32) {
	if s == nil || s.deps.Pool == nil || siteID <= 0 {
		return
	}
	now := s.deps.Now()
	s.viewMu.Lock()
	if last, ok := s.viewed[siteID]; ok && now.Sub(last) < demoViewedThrottle {
		s.viewMu.Unlock()
		return
	}
	s.viewed[siteID] = now
	s.viewMu.Unlock()
	if err := store.TouchDemoViewed(s.ctx, s.deps.Pool, siteID, now); err != nil {
		s.viewMu.Lock()
		if s.viewed[siteID].Equal(now) {
			delete(s.viewed, siteID)
		}
		s.viewMu.Unlock()
	}
}

// Heartbeat marks a demo as actively watched and records the dashboard view.
func (s *Service) Heartbeat(siteID int32) {
	if s == nil || siteID <= 0 {
		return
	}
	now := s.deps.Now()
	s.viewMu.Lock()
	s.watched[siteID] = now
	s.viewMu.Unlock()
	s.Viewed(siteID)
}

// Demo realtime and busy traffic assume one scheduler instance: heartbeats
// received by another process are not visible to the process running this job.
func (s *Service) watching(siteID int32, now time.Time) bool {
	if s == nil {
		return false
	}
	s.viewMu.Lock()
	last, ok := s.watched[siteID]
	s.viewMu.Unlock()
	return ok && now.Sub(last) <= heartbeatTTL
}

func (s *Service) pruneWatched(now time.Time) {
	s.viewMu.Lock()
	for siteID, last := range s.watched {
		if now.Sub(last) > watchPruneAfter {
			delete(s.watched, siteID)
		}
	}
	s.viewMu.Unlock()
}

// Tick advances every recently viewed, fully seeded demo to the current time.
func (s *Service) Tick(ctx context.Context) error {
	if s == nil || s.deps.Pool == nil || s.deps.Registry == nil || s.deps.Dict == nil ||
		s.deps.Sessions == nil || s.deps.Writer == nil || s.deps.Blobs == nil || s.deps.Index == nil {
		return errors.New("demo live service is not configured")
	}
	s.tickMu.Lock()
	defer s.tickMu.Unlock()

	now := s.deps.Now()
	s.pruneWatched(now)
	states, err := store.ActiveDemoSites(ctx, s.deps.Pool, now.Add(-demoActiveWindow))
	if err != nil {
		return err
	}
	var tickErrs []error
	for _, state := range states {
		if state.SeededAt == nil || state.LiveThrough == nil || (!now.After(*state.LiveThrough) && !s.watching(state.SiteID, now)) {
			continue
		}
		if err := s.tickSite(ctx, state, now); err != nil {
			wrapped := fmt.Errorf("advance demo site %d: %w", state.SiteID, err)
			slog.Error("demo live tick failed", "site_id", state.SiteID, "error", err)
			tickErrs = append(tickErrs, wrapped)
		}
	}
	trackedSites := make(map[int32]struct{}, len(s.pending)+len(s.busyPending))
	for siteID := range s.pending {
		trackedSites[siteID] = struct{}{}
	}
	for siteID := range s.busyPending {
		trackedSites[siteID] = struct{}{}
	}
	for siteID := range trackedSites {
		var active bool
		if err := s.deps.Pool.QueryRow(ctx, `select exists(select 1 from sites where id=$1 and is_demo and deleted_at is null)`, siteID).Scan(&active); err != nil {
			tickErrs = append(tickErrs, fmt.Errorf("check pending demo site %d: %w", siteID, err))
			continue
		}
		if !active {
			s.dropPending(siteID)
		}
	}
	return errors.Join(tickErrs...)
}

func (s *Service) tickSite(ctx context.Context, state store.DemoState, now time.Time) error {
	site, err := s.site(state.SiteID)
	if err != nil {
		return err
	}
	from := *state.LiveThrough
	if cursor := s.cursors[site.ID]; cursor.After(from) {
		from = cursor
	}
	anchor := localMidnight(*state.SeededAt, site.TZ)
	model := synth.NewDemo(synth.DemoConfig{
		Seed: s.demoSeed(site.ID), TZ: site.TZ, Visitors: s.deps.Visitors, Anchor: anchor,
	})
	if now.Sub(from) > demoGapThreshold {
		gapEnd := now.Add(-demoLiveTail)
		if err := s.writeHistory(ctx, site, model.Visits(from, gapEnd)); err != nil {
			return fmt.Errorf("backfill gap: %w", err)
		}
		s.cursors[site.ID] = gapEnd
		if err := s.closePastDays(ctx, site, from, gapEnd); err != nil {
			return err
		}
		from = gapEnd
	}
	if err := s.writeLive(ctx, site, model.Visits(from, now), now); err != nil {
		return err
	}
	if err := s.writeBusy(ctx, site, model, now); err != nil {
		return err
	}
	if err := s.setDemoLiveThrough(ctx, site.ID, now); err != nil {
		return err
	}
	delete(s.cursors, site.ID)
	return nil
}

func (s *Service) writeBusy(ctx context.Context, site sites.Site, model *synth.Demo, now time.Time) error {
	if !s.watching(site.ID, now) {
		delete(s.busyPending, site.ID)
		delete(s.busyWatching, site.ID)
		return nil
	}
	warm := !s.busyWatching[site.ID]
	if err := s.writePending(ctx, site, nil, now, s.busyPending); err != nil {
		return err
	}
	active, _ := s.deps.Sessions.ActiveSince(site.ID, now.Add(-realtime.LiveWindow))
	need := synth.BusyTarget(now.In(site.TZ)) - active
	if need <= 0 {
		s.busyWatching[site.ID] = true
		return nil
	}
	visits := make([]synth.DemoVisit, 0, need)
	for range need {
		sequence := s.busySeq[site.ID]
		s.busySeq[site.ID] = sequence + 1
		visitKey := busyVisitKey(site.ID, sequence)
		age := busyVisitAge(visitKey, warm)
		visit := model.BusyVisit(now.Add(-age), visitKey)
		lastActivity := visit.Hits[len(visit.Hits)-1].Offset
		if warm && age > lastActivity+realtime.LiveWindow-time.Second {
			visit.Start = now.Add(-(lastActivity + realtime.LiveWindow - time.Second))
		}
		visits = append(visits, visit)
	}
	if err := s.writePending(ctx, site, visits, now, s.busyPending); err != nil {
		return err
	}
	s.busyWatching[site.ID] = true
	return nil
}

func busyVisitKey(siteID int32, sequence uint64) uint64 {
	x := uint64(uint32(siteID))<<32 ^ sequence
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func busyVisitAge(key uint64, warm bool) time.Duration {
	if warm {
		return time.Minute + time.Duration(key%841)*time.Second
	}
	return time.Duration(key%16) * time.Second
}

func (s *Service) setDemoLiveThrough(ctx context.Context, siteID int32, at time.Time) error {
	if s.deps.SetLiveThrough != nil {
		return s.deps.SetLiveThrough(ctx, siteID, at)
	}
	return store.SetDemoLiveThrough(ctx, s.deps.Pool, siteID, at)
}

func (s *Service) writeLive(ctx context.Context, site sites.Site, visits []synth.DemoVisit, through time.Time) error {
	s.cursors[site.ID] = through
	return s.writePending(ctx, site, visits, through, s.pending)
}

func (s *Service) writePending(ctx context.Context, site sites.Site, visits []synth.DemoVisit, through time.Time, pendingBySite map[int32][]pendingVisit) error {
	work := append([]pendingVisit(nil), pendingBySite[site.ID]...)
	for _, visit := range visits {
		work = append(work, pendingVisit{visit: visit})
	}
	if len(work) > 0 {
		pendingBySite[site.ID] = work
	}
	if len(work) == 0 {
		delete(pendingBySite, site.ID)
		return nil
	}
	dimensionVisits := make([]synth.DemoVisit, len(work))
	for i := range work {
		dimensionVisits[i] = work[i].visit
	}
	classifier, err := enrich.NewSourceClassifier()
	if err != nil {
		return fmt.Errorf("load source classifier: %w", err)
	}
	dims, sources, mediums, err := s.demoDimensions(ctx, site.ID, dimensionVisits, classifier)
	if err != nil {
		return err
	}

	hits := make([]liveHit, 0)
	for visitIndex := range work {
		for hitIndex := work[visitIndex].next; hitIndex < len(work[visitIndex].visit.Hits); hitIndex++ {
			at := work[visitIndex].visit.Start.Add(work[visitIndex].visit.Hits[hitIndex].Offset)
			if at.After(through) {
				break
			}
			hits = append(hits, liveHit{visit: visitIndex, hit: hitIndex, at: at})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].at.Before(hits[j].at) })
	for _, item := range hits {
		pending := &pendingBySite[site.ID][item.visit]
		syntheticHit := pending.visit.Hits[item.hit]
		result, err := s.enqueueLiveHit(ctx, site, pending.visit, syntheticHit, item.at, dims, sources[item.visit], mediums[item.visit])
		if err != nil {
			return err
		}
		pending.next = item.hit + 1
		if pending.next == len(pending.visit.Hits) {
			s.deps.Sessions.Expire(pending.visit.Start.Add(session.IdleTimeout + time.Nanosecond))
		}
		if result.SessionID != 0 && pending.sessionID == 0 {
			pending.sessionID = result.SessionID
			pending.localDay = salts.LocalDay(item.at, site.TZ)
			pending.pathID = dims.path[syntheticHit.Path]
		}
		if pending.visit.Replay && pending.sessionID != 0 && !pending.replayWritten {
			if err := s.writeLiveReplay(ctx, site.ID, pending); err != nil {
				return err
			}
			pending.replayWritten = true
		}
	}

	remaining := pendingBySite[site.ID][:0]
	for _, pending := range pendingBySite[site.ID] {
		if pending.next < len(pending.visit.Hits) {
			remaining = append(remaining, pending)
		}
	}
	if len(remaining) == 0 {
		delete(pendingBySite, site.ID)
	} else {
		pendingBySite[site.ID] = append([]pendingVisit(nil), remaining...)
	}
	return nil
}

func (s *Service) enqueueLiveHit(
	ctx context.Context,
	site sites.Site,
	visit synth.DemoVisit,
	syntheticHit synth.DemoHit,
	timestamp time.Time,
	dims visitDimensions,
	source, medium string,
) (session.Result, error) {
	visitorID := int64(visit.VisitorKey &^ (uint64(1) << 63))
	nameID := int32(0)
	if syntheticHit.Kind == 1 {
		nameID = dims.name[syntheticHit.Name]
	}
	hit := session.Hit{
		SiteID: site.ID, Retention: site.Retention, TS: timestamp, LocalDay: salts.LocalDay(timestamp, site.TZ),
		VisitorID: visitorID, Kind: syntheticHit.Kind, PathID: dims.path[syntheticHit.Path], NameID: nameID,
		Source: dims.source[source], UTMSource: dims.utmSource[visit.UTMSource], Medium: dims.medium[medium], Campaign: dims.campaign[visit.UTMCampaign],
		Country: visit.Country, Region: dims.region[visit.Region], City: dims.city[visit.City],
		Browser: dims.browser[visit.Browser], OS: dims.os[visit.OS], BrowserVer: dims.browserVersion[visit.BrowserVer], OSVer: dims.osVersion[visit.OSVer],
		Language: dims.language[visit.Language], Hostname: dims.hostname[visit.Host], ScreenBucket: enrich.ScreenBucket(visit.ScreenW), Device: visit.Device,
		EngagementMS: syntheticHit.EngagementMS, ScrollPct: syntheticHit.ScrollPct, JoinOnly: syntheticHit.Name == "Web Vitals",
	}
	result := s.deps.Sessions.Touch(hit)
	if hit.JoinOnly && result.SessionID == 0 {
		return result, nil
	}
	event := writer.Event{
		Hit: hit, SessionID: result.SessionID, Screen: int16(visit.ScreenW), ReferrerID: dims.referrer[visit.Referrer],
		UTMContent: dims.content[visit.UTMContent], UTMTerm: dims.term[visit.UTMTerm], RevenueMicros: syntheticHit.RevenueMicros, Props: syntheticHit.Props,
	}
	event.VisitorID = result.VisitorID
	if event.RevenueMicros > 0 {
		event.RevenueCurrency = "USD"
	}
	if visit.Profile != nil {
		event.ProfileExternalID = visit.Profile.ExternalID
		event.ProfileTraits = visit.Profile.Traits
		if linker, ok := s.deps.Sessions.(session.ProfileLinker); ok {
			_ = linker.LinkProfile(site.ID, result.SessionID, visit.Profile.ExternalID)
		}
	}
	if s.deps.Activity != nil && syntheticHit.Kind != 2 {
		s.deps.Activity.RecordActivity(session.Activity{
			SiteID: site.ID, Timestamp: timestamp, VisitorID: result.VisitorID, Kind: syntheticHit.Kind,
			PathID: hit.PathID, NameID: hit.NameID, Country: hit.Country, CityID: hit.City,
			Device: hit.Device, BrowserID: hit.Browser, SourceID: hit.Source,
		})
	}
	if err := enqueueEvent(ctx, s.deps.Writer, event); err != nil {
		return session.Result{}, err
	}
	if syntheticHit.Kind == 0 && len(syntheticHit.Clicks) > 0 {
		if err := enqueueHeatmap(ctx, s.deps.Writer, writer.HeatmapEvent{
			SiteID: site.ID, LocalDay: hit.LocalDay, PathID: hit.PathID, ViewportWidth: visit.ScreenW, Clicks: syntheticHit.Clicks,
		}); err != nil {
			return session.Result{}, err
		}
	}
	return result, nil
}

func enqueueEvent(ctx context.Context, output *writer.Writer, event writer.Event) error {
	retry := time.NewTicker(time.Millisecond)
	defer retry.Stop()
	for !output.Enqueue(event) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-retry.C:
		}
	}
	return nil
}

func enqueueHeatmap(ctx context.Context, output *writer.Writer, event writer.HeatmapEvent) error {
	retry := time.NewTicker(time.Millisecond)
	defer retry.Stop()
	for !output.EnqueueHeatmap(event) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-retry.C:
		}
	}
	return nil
}

func (s *Service) writeLiveReplay(ctx context.Context, siteID int32, pending *pendingVisit) error {
	key := replay.SessionKey(siteID, pending.localDay, pending.sessionID)
	written, err := s.deps.Blobs.Append(ctx, key, bytes.NewReader(synth.DemoRecording(pending.visit)))
	if err != nil {
		return fmt.Errorf("write live demo replay: %w", err)
	}
	s.deps.Index.Add(replay.IndexUpdate{
		SiteID: siteID, SessionID: pending.sessionID, Day: pending.localDay, At: pending.visit.Start,
		Bytes: written, Page: pending.pathID != 0, Trigger: "manual",
	})
	return nil
}

func (s *Service) closePastDays(ctx context.Context, site sites.Site, from, through time.Time) error {
	day := localMidnight(from, site.TZ)
	for next := day.AddDate(0, 0, 1); !next.After(through); next = next.AddDate(0, 0, 1) {
		civilDay := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
		if _, err := closer.CloseDay(ctx, s.deps.Pool, []int32{site.ID}, civilDay); err != nil {
			return fmt.Errorf("close demo day %s: %w", civilDay.Format(time.DateOnly), err)
		}
		day = next
	}
	return nil
}

func (s *Service) demoSeed(siteID int32) int64 {
	if s.deps.Seed != 0 {
		return s.deps.Seed
	}
	return int64(siteID)
}

func localMidnight(at time.Time, location *time.Location) time.Time {
	if location == nil {
		location = time.UTC
	}
	local := at.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
}
