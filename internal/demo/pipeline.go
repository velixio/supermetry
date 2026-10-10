package demo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/enrich"
	"supermetry/internal/privacy"
	"supermetry/internal/replay"
	"supermetry/internal/salts"
	"supermetry/internal/session"
	"supermetry/internal/sites"
	"supermetry/internal/synth"
	"supermetry/internal/writer"
)

const demoQueueSize = 1 << 16

type oneSiteRegistry struct{ site sites.Site }

func (r oneSiteRegistry) ByPublicID(id string) (sites.Site, bool) {
	return r.site, id == r.site.PublicID
}
func (r oneSiteRegistry) Site(id int32) (sites.Site, bool) { return r.site, id == r.site.ID }
func (r oneSiteRegistry) All() []sites.Site                { return []sites.Site{r.site} }

type visitDimensions struct {
	path, referrer, source, utmSource, medium, campaign, content, term map[string]int32
	name, region, city, browser, os, browserVersion, osVersion         map[string]int32
	language, hostname                                                 map[string]int32
}

func (s *Service) writeHistory(ctx context.Context, site sites.Site, visits []synth.DemoVisit) error {
	if len(visits) == 0 {
		return nil
	}
	if err := db.EnsurePartitions(ctx, s.deps.Pool, visits[0].Start.AddDate(0, 0, -1), visits[len(visits)-1].Start.AddDate(0, 0, 8)); err != nil {
		return fmt.Errorf("ensure demo partitions: %w", err)
	}
	classifier, err := enrich.NewSourceClassifier()
	if err != nil {
		return fmt.Errorf("load source classifier: %w", err)
	}
	dimensions, sources, mediums, err := s.demoDimensions(ctx, site.ID, visits, classifier)
	if err != nil {
		return err
	}

	sessions := session.NewMemory(16, nil)
	w := writer.New(s.deps.Pool, sessions, demoQueueSize, 20*time.Millisecond, 20*time.Millisecond)
	w.SetTrackingPolicy(privacy.Identified, oneSiteRegistry{site: site})
	writerCtx, stopWriter := context.WithCancel(context.Background())
	writerDone := make(chan error, 1)
	go func() { writerDone <- w.Run(writerCtx) }()

	replays, writeErr := s.enqueueVisits(ctx, site, visits, dimensions, sources, mediums, sessions, w)
	stopWriter()
	flushErr := <-writerDone
	if writeErr != nil || flushErr != nil {
		return errors.Join(writeErr, flushErr)
	}
	return s.writeReplays(ctx, site, replays)
}

type demoReplay struct {
	visit     synth.DemoVisit
	sessionID int64
	localDay  time.Time
	pathID    int32
}

func (s *Service) enqueueVisits(
	ctx context.Context,
	site sites.Site,
	visits []synth.DemoVisit,
	dims visitDimensions,
	sources, mediums []string,
	sessions *session.Memory,
	w *writer.Writer,
) ([]demoReplay, error) {
	replays := make([]demoReplay, 0)
	retryTimer := time.NewTimer(time.Hour)
	if !retryTimer.Stop() {
		<-retryTimer.C
	}
	defer retryTimer.Stop()
	for visitIndex, visit := range visits {
		visitorID := int64(visit.VisitorKey &^ (uint64(1) << 63))
		var replaySessionID int64
		var replayDay time.Time
		var replayPathID int32
		for _, syntheticHit := range visit.Hits {
			timestamp := visit.Start.Add(syntheticHit.Offset)
			localDay := salts.LocalDay(timestamp, site.TZ)
			nameID := int32(0)
			if syntheticHit.Kind == 1 {
				nameID = dims.name[syntheticHit.Name]
			}
			hit := session.Hit{
				SiteID: site.ID, Retention: site.Retention, TS: timestamp, LocalDay: localDay,
				VisitorID: visitorID, Kind: syntheticHit.Kind,
				PathID: dims.path[syntheticHit.Path], NameID: nameID,
				Source: dims.source[sources[visitIndex]], UTMSource: dims.utmSource[visit.UTMSource],
				Medium: dims.medium[mediums[visitIndex]], Campaign: dims.campaign[visit.UTMCampaign],
				Country: visit.Country, Region: dims.region[visit.Region], City: dims.city[visit.City],
				Browser: dims.browser[visit.Browser], OS: dims.os[visit.OS],
				BrowserVer: dims.browserVersion[visit.BrowserVer], OSVer: dims.osVersion[visit.OSVer],
				Language: dims.language[visit.Language], Hostname: dims.hostname[visit.Host],
				ScreenBucket: enrich.ScreenBucket(visit.ScreenW), Device: visit.Device,
				EngagementMS: syntheticHit.EngagementMS, ScrollPct: syntheticHit.ScrollPct,
				JoinOnly: syntheticHit.Name == "Web Vitals",
			}
			result := sessions.Touch(hit)
			if hit.JoinOnly && result.SessionID == 0 {
				continue
			}
			if replaySessionID == 0 {
				replaySessionID, replayDay, replayPathID = result.SessionID, localDay, hit.PathID
			}
			event := writer.Event{
				Hit: hit, SessionID: result.SessionID, Screen: int16(visit.ScreenW),
				ReferrerID: dims.referrer[visit.Referrer], UTMContent: dims.content[visit.UTMContent], UTMTerm: dims.term[visit.UTMTerm],
				RevenueMicros: syntheticHit.RevenueMicros, Props: syntheticHit.Props,
			}
			event.VisitorID = result.VisitorID
			if event.RevenueMicros > 0 {
				event.RevenueCurrency = "USD"
			}
			if visit.Profile != nil {
				event.ProfileExternalID = visit.Profile.ExternalID
				event.ProfileTraits = visit.Profile.Traits
				_ = sessions.LinkProfile(site.ID, result.SessionID, visit.Profile.ExternalID)
			}
			for !w.Enqueue(event) {
				retryTimer.Reset(time.Millisecond)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-retryTimer.C:
				}
			}
			if syntheticHit.Kind == 0 && len(syntheticHit.Clicks) > 0 {
				heatmap := writer.HeatmapEvent{
					SiteID: site.ID, LocalDay: localDay, PathID: hit.PathID,
					ViewportWidth: visit.ScreenW, Clicks: syntheticHit.Clicks,
				}
				for !w.EnqueueHeatmap(heatmap) {
					retryTimer.Reset(time.Millisecond)
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-retryTimer.C:
					}
				}
			}
		}
		if visit.Replay && replaySessionID != 0 {
			replays = append(replays, demoReplay{visit: visit, sessionID: replaySessionID, localDay: replayDay, pathID: replayPathID})
		}
		// DemoVisit is the session boundary even when the deterministic model
		// selects the same returning visitor twice within the idle timeout.
		sessions.Expire(visit.Start.Add(session.IdleTimeout + time.Nanosecond))
		if s.deps.SeedVisit != nil {
			s.deps.SeedVisit(visitIndex+1, len(visits))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
	return replays, nil
}

func (s *Service) writeReplays(ctx context.Context, site sites.Site, recordings []demoReplay) error {
	if len(recordings) == 0 {
		return nil
	}
	sessionIDs := make([]int64, 0, len(recordings))
	for _, recording := range recordings {
		key := replay.SessionKey(site.ID, recording.localDay, recording.sessionID)
		written, err := s.deps.Blobs.Append(ctx, key, bytes.NewReader(synth.DemoRecording(recording.visit)))
		if err != nil {
			return fmt.Errorf("write demo replay: %w", err)
		}
		var profileID *int64
		if recording.visit.Profile != nil {
			var id int64
			if err := s.deps.Pool.QueryRow(ctx, `select id from profiles where site_id=$1 and external_id=$2`, site.ID, recording.visit.Profile.ExternalID).Scan(&id); err != nil {
				return fmt.Errorf("load demo replay profile: %w", err)
			}
			profileID = &id
		}
		s.deps.Index.Add(replay.IndexUpdate{
			SiteID: site.ID, SessionID: recording.sessionID, Day: recording.localDay,
			At: recording.visit.Start, Bytes: written, Page: recording.pathID != 0,
			Trigger: "manual", ProfileID: profileID,
		})
		sessionIDs = append(sessionIDs, recording.sessionID)
	}
	return s.waitForReplayIndex(ctx, site.ID, sessionIDs)
}

func (s *Service) waitForReplayIndex(ctx context.Context, siteID int32, sessionIDs []int64) error {
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		if err := s.deps.Pool.QueryRow(waitCtx, `
			select count(*) from replay_sessions
			where site_id=$1 and session_id=any($2)`, siteID, sessionIDs).Scan(&count); err != nil {
			return fmt.Errorf("confirm demo replay index: %w", err)
		}
		if count == len(sessionIDs) {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("confirm demo replay index: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func (s *Service) demoDimensions(ctx context.Context, siteID int32, visits []synth.DemoVisit, classifier *enrich.SourceClassifier) (visitDimensions, []string, []string, error) {
	values := make(map[dict.Kind][]string)
	seen := make(map[dict.Kind]map[string]struct{})
	add := func(kind dict.Kind, value string) {
		if value == "" {
			return
		}
		if seen[kind] == nil {
			seen[kind] = make(map[string]struct{})
		}
		if _, ok := seen[kind][value]; ok {
			return
		}
		seen[kind][value] = struct{}{}
		values[kind] = append(values[kind], value)
	}
	sources := make([]string, len(visits))
	mediums := make([]string, len(visits))
	for i, visit := range visits {
		sources[i], mediums[i] = classifier.Source(visit.Referrer, "https://"+visit.Host+"/", enrich.UTM{Source: visit.UTMSource, Medium: visit.UTMMedium})
		if sources[i] == "" {
			sources[i] = "(direct)"
		}
		for kind, value := range map[dict.Kind]string{
			dict.Referrer: visit.Referrer, dict.Source: sources[i], dict.Medium: mediums[i],
			dict.Campaign: visit.UTMCampaign, dict.UTMContent: visit.UTMContent, dict.UTMTerm: visit.UTMTerm,
			dict.Region: visit.Region, dict.City: visit.City, dict.Browser: visit.Browser, dict.OS: visit.OS,
			dict.BrowserVersion: visit.BrowserVer, dict.OSVersion: visit.OSVer, dict.Language: visit.Language, dict.Hostname: visit.Host,
		} {
			add(kind, value)
		}
		add(dict.Source, visit.UTMSource)
		for _, hit := range visit.Hits {
			add(dict.Path, hit.Path)
			if hit.Kind == 1 {
				add(dict.EventName, hit.Name)
			}
		}
	}
	encoded := make(map[dict.Kind]map[string]int32, len(values))
	for kind, stringsForKind := range values {
		ids, err := s.deps.Dict.IDsForSite(ctx, siteID, kind, stringsForKind)
		if err != nil {
			return visitDimensions{}, nil, nil, fmt.Errorf("encode demo dimension %d: %w", kind, err)
		}
		encoded[kind] = make(map[string]int32, len(ids))
		for i, value := range stringsForKind {
			encoded[kind][value] = ids[i]
		}
	}
	return visitDimensions{
		path: encoded[dict.Path], referrer: encoded[dict.Referrer], source: encoded[dict.Source], utmSource: encoded[dict.Source],
		medium: encoded[dict.Medium], campaign: encoded[dict.Campaign], content: encoded[dict.UTMContent], term: encoded[dict.UTMTerm],
		name: encoded[dict.EventName], region: encoded[dict.Region], city: encoded[dict.City], browser: encoded[dict.Browser], os: encoded[dict.OS],
		browserVersion: encoded[dict.BrowserVersion], osVersion: encoded[dict.OSVersion], language: encoded[dict.Language], hostname: encoded[dict.Hostname],
	}, sources, mediums, nil
}
