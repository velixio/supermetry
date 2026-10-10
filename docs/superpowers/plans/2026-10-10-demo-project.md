# Demo Project Implementation Plan

> **For agentic workers:** run each task with `PLAN=docs/superpowers/plans/2026-10-10-demo-project.md SPEC=docs/superpowers/specs/2026-10-10-demo-project-design.md scripts/codex-task.sh <n>`. The runner gates (gofmt, vet, go test, web gate) and commits; implementers never run git write commands. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Each organization can add a dedicated demo project, filled with 90 days of realistic synthetic data plus a live simulator, so that every dashboard feature shows data before the tracker is installed.

**Architecture:** A callback-based demo traffic model in `internal/synth` (beside the unchanged bench generator) produces sessions with every field set. `internal/demo` writes history through a private writer and session-store pipeline (as `cmd/supermetry/load.go` does), closes past days, and creates example settings. A scheduler job, `demo-live`, feeds new visits through the shared session store and writer, so Realtime works. Demo sites carry `is_demo`: ingest and replay reject them, and the writer's privacy gate lets their synthetic identity fields through.

**Tech Stack:** Go 1.26, pgx/v5, PostgreSQL (forward-only migrations), `net/http`; web: Bun, Vite, React, TS, TanStack Router/Query, shadcn/ui, Tailwind; API client generated from `internal/api/openapi.yaml`.

**Spec:** [docs/superpowers/specs/2026-10-10-demo-project-design.md](../specs/2026-10-10-demo-project-design.md)

## Global Constraints

- Hard rules in AGENTS.md apply: no ClickHouse, Kafka, separate workers or Node in production; never touch `knowledge/`; never copy AGPL code.
- Migration file: `internal/db/migrations/0041_demo_project.sql`, transactional, forward-only.
- Demo window: 90 site-local days ending now; default `Visitors` = 150; retention class 90.
- Demo hosts: `www.demo.example`, `docs.demo.example`. Never use real domains for demo hosts. No email addresses in demo data other than `@demo.example`.
- Revenue currency for demo: `USD`, state configured.
- `tools/gen` fixtures must stay byte-identical (`TestFixturesUpToDate`).
- Never log or persist IPs or User-Agent strings. Demo visitors have no IP or UA; use synthetic IDs.
- Web vitals props stay within ingest caps: `lcp` ≤ 60000, `cls` (×1000) ≤ 10000, `inp` ≤ 60000.
- The demo alert is created **disabled** with no channels. Create no report subscriptions or shared links for the demo.
- Commit messages: conventional, no trailers, no mention of any AI tool or assistant.

## Review Focus

1. **A real tracker sends hits to a demo `public_id`** (a user copies the demo snippet). The hits must be rejected like an unknown site, and none may reach `events`. Test: Task 121.
2. **The server restarts mid-seed.** On startup the half-seeded demo must be erased and reseeded, with no duplicated sessions. Test: Task 123.
3. **A user comes back after days away, so the simulator gap spans several local days.** The gap must be backfilled once, the newly past days closed, and no window double-counted. Test: Task 125.
4. **Two clicks on "Explore with demo data", or two tabs.** The second request gets 409 `demo_exists` with the existing id, not a second demo. Test: Task 126 (and the unique index in Task 121).
5. **A demo site in a zone with a :30 or :45 offset** (Asia/Kolkata, Asia/Kathmandu). Day boundaries, closing and the diurnal curve must use local days. Test: Task 122 (window partition in Kolkata) and Task 123 (seed in Asia/Kathmandu).

## File Map

| Path | Task | Responsibility |
|---|---|---|
| `internal/db/migrations/0041_demo_project.sql` | 121 | Columns and index from spec section 3 |
| `internal/sites/registry.go`, `internal/sites/file.go` | 121 | `Site.Demo` |
| `internal/platform/store/sites.go` | 121 | select `is_demo`; `CreateDemoSite`; demo column setters |
| `internal/ingest/handler.go`, `internal/replay/handler.go` | 121 | reject demo sites |
| `internal/writer/tracking.go` | 121 | demo bypass in `identityAllowed` / `pathsAllowed` |
| `internal/synth/demo.go`, `demo_model.go`, `demo_test.go` | 122 | demo traffic model |
| `internal/synth/rrweb.go`, `rrweb_test.go` | 122 | synthetic rrweb recordings |
| `internal/demo/seed.go`, `pipeline.go`, `service.go`, `seed_test.go` | 123 | create, seed, resume |
| `internal/demo/examples.go`, `examples_test.go` | 124 | heatmaps, replay, example settings |
| `internal/platform/store/resources.go` (new) | 124 | goal, funnel, annotation and segment insert functions shared with the handlers |
| `internal/demo/live.go`, `live_test.go`; `cmd/supermetry/serve.go` | 125 | live simulator job and wiring |
| `internal/platform/http/handlers_demo.go`, `private.go`, `handlers_account.go`, `handlers_sites.go`; `internal/api/openapi.yaml` | 126 | API |
| `web/src/features/demo/*`, `routes/app.index.tsx`, `components/Sidebar.tsx`, site settings, `src/lib/api.gen.ts` | 221 | UI |

---

### Task 121: Schema, registry flag, gates

**Files:**
- Create: `internal/db/migrations/0041_demo_project.sql`
- Modify: `internal/sites/registry.go` (`Site` struct), `internal/sites/file.go` (`fileSite` + mapping), `internal/platform/store/sites.go` (`ActiveSites` select/scan; new funcs), `internal/ingest/handler.go`, `internal/replay/handler.go`, `internal/writer/tracking.go`
- Test: `internal/platform/store/sites_demo_test.go`, `internal/ingest/demo_test.go`, `internal/replay/demo_test.go`, `internal/writer/tracking_demo_test.go`

**Interfaces:**
- Produces:
  - `sites.Site.Demo bool`
  - `store.CreateDemoSite(ctx, tx pgx.Tx, s store.Site, createdAt time.Time) (store.Site, error)`. It inserts with `is_demo=true` and `profiles_enabled`/`heatmaps_enabled` true in `settings`, replay at 100%, and `revenue_currency='USD'`. It returns `store.ErrDemoExists` on a unique violation of `sites_one_demo_per_org`.
  - `store.DemoSiteForOrg(ctx, q, orgID int64) (store.Site, bool, error)`
  - `store.SetDemoSeeded(ctx, q, siteID int32, at time.Time) error`
  - `store.SetDemoLiveThrough(ctx, q, siteID int32, at time.Time) error`
  - `store.TouchDemoViewed(ctx, q, siteID int32, at time.Time) error`
  - `store.UnseededDemoSites(ctx, q) ([]int32, error)`
  - `store.ActiveDemoSites(ctx, q, viewedSince time.Time) ([]store.DemoState, error)`, where `DemoState{SiteID int32; SeededAt, LiveThrough *time.Time}`
  - `store.DismissDemoPrompt(ctx, q, userID int64, at time.Time) error`
  - `store.DemoPromptDismissed(ctx, q, userID int64) (bool, error)`
  - `store.Site` gains `IsDemo bool`, `DemoSeededAt *time.Time`.
- Uses `q` as whatever query interface `store` already uses (`pgx.Tx` or the pool interface in that file). Match the existing pattern.

- [ ] **Step 1: Write the migration** with exactly the SQL in spec section 3.
- [ ] **Step 2: Write failing store tests** (testpg + `db.Migrate`):

```go
func TestCreateDemoSiteOnePerOrg(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil { t.Fatal(err) }
	orgID := mustOrg(t, pool) // helper: insert user + org via store.CreateUser/CreateOrg
	created := time.Now().AddDate(0, 0, -89)
	tx := mustBegin(t, pool)
	s1, err := store.CreateDemoSite(ctx, tx, store.Site{OrgID: orgID, Name: "Demo project", Timezone: "UTC", RetentionDays: 90, AllowedHosts: []string{"www.demo.example", "docs.demo.example"}}, created)
	if err != nil || !s1.IsDemo { t.Fatalf("create: %v %+v", err, s1) }
	mustCommit(t, tx)
	tx = mustBegin(t, pool)
	_, err = store.CreateDemoSite(ctx, tx, store.Site{OrgID: orgID, Name: "Demo project", Timezone: "UTC", RetentionDays: 90}, created)
	if !errors.Is(err, store.ErrDemoExists) { t.Fatalf("second demo: got %v, want ErrDemoExists", err) }
	_ = tx.Rollback(ctx)
	got, ok, err := store.DemoSiteForOrg(ctx, pool, orgID)
	if err != nil || !ok || got.ID != s1.ID { t.Fatalf("lookup: %v %v %+v", err, ok, got) }
}
```

  Also test: `ActiveSites` returns `Demo=true` for it; `DismissDemoPrompt` then `DemoPromptDismissed` is true; after a soft delete a new demo can be created.
- [ ] **Step 3: Write failing gate tests.**
  - **Ingest:** build the handler as the existing ingest tests do, with a registry holding a demo site. POST a valid pageview for its `public_id`. Expect the same status and body as for an unknown `public_id`, and that the writer receives nothing (the existing fake writer or reservation counter).
  - **Replay:** `Start` and `Chunk` for a demo site respond as for an unknown site.
  - **Writer:** in anonymous mode, with a registry holding a demo site (Demo, ProfilesEnabled=false) and a normal site, `suppressRestrictedIdentity` keeps `Props`, `UTMSource`, `ProfileExternalID` and `PathID` for the demo event and clears them for the normal one.
- [ ] **Step 4: Run them and see them fail:** `go test ./internal/platform/store/ ./internal/ingest/ ./internal/replay/ ./internal/writer/ -run 'Demo' -count=1`
- [ ] **Step 5: Implement.**
  - In `identityAllowed`: `if ok && site.Demo { return true }` before the mode check. Restructure so the registry lookup happens first. Do the same in `pathsAllowed`.
  - In ingest and replay, reject right after the site lookup. Reuse the unknown-site branch, so the response is identical.
- [ ] **Step 6: Run the package tests plus `go test ./...` and see them pass.**

---

### Task 122: Demo traffic model and rrweb recordings in `internal/synth`

**Files:**
- Create: `internal/synth/demo.go` (types, `NewDemo`, `Visits`), `internal/synth/demo_model.go` (tables: pages, channels, geo, devices, events), `internal/synth/rrweb.go`, `internal/synth/demo_test.go`, `internal/synth/rrweb_test.go`
- Must not change: any existing `internal/synth` file's behavior (`tools/gen` `TestFixturesUpToDate` must pass)

**Interfaces:**
- Produces exactly the types and functions in spec section 5: `DemoConfig`, `DemoVisit`, `DemoHit`, `DemoProfile`, `NewDemo(DemoConfig) *Demo`, `(*Demo).Visits(from, to time.Time) []DemoVisit`, `DemoRecording(v DemoVisit) []byte`.
- Exported constants used by later tasks:
  - `DemoHosts = []string{"www.demo.example", "docs.demo.example"}`
  - `DemoWindowDays = 90`
  - `DemoCampaign = "autumn-launch"`
  - `DemoCampaignDaysAgo = 20`
  - `DemoDipDaysAgo = 40`

**Determinism design (required):**
- Generate per site-local day. The day's RNG is seeded from `(Seed, localDate)`.
- A day's sessions are fully determined by its date. `Visits(from, to)` generates every overlapping local day and filters by `Start`, so adjacent windows partition exactly.
- Returning visitors: derive `VisitorKey` from `(Seed, poolIndex)`, where the pool index is drawn from a pool that grows with the day index, so returns reuse earlier keys.
- Profile identity: derive from `VisitorKey` (a visitor who signs up on day N gets `user_<hash>`, and every later visit by that key carries the same profile). Decide "has signed up by day N" deterministically from `(Seed, VisitorKey)` and a signup day, so it doesn't depend on window order.
- Volume and campaign/dip days are relative to the window end. Anchor them to an `Anchor` date in `DemoConfig` (add `Anchor time.Time`, the seed day; zero means today). Without this, the history would shift as days pass, so the generated past would change between the seed and the live run.

- [ ] **Step 1: Write failing tests:**

```go
func TestDemoVisitsDeterministicAndPartitioned(t *testing.T) {
	tz, _ := time.LoadLocation("Asia/Kolkata")
	anchor := time.Date(2026, 10, 10, 0, 0, 0, 0, tz)
	d := NewDemo(DemoConfig{Seed: 7, TZ: tz, Visitors: 150, Anchor: anchor})
	from, mid, to := anchor.AddDate(0, 0, -3), anchor.AddDate(0, 0, -1).Add(5*time.Hour+17*time.Minute), anchor.AddDate(0, 0, 1)
	all := d.Visits(from, to)
	a, b := d.Visits(from, mid), d.Visits(mid, to)
	if len(a)+len(b) != len(all) { t.Fatalf("partition: %d+%d != %d", len(a), len(b), len(all)) }
	if !reflect.DeepEqual(append(a, b...), all) { t.Fatal("windows differ from whole") }
	if !reflect.DeepEqual(all, NewDemo(DemoConfig{Seed: 7, TZ: tz, Visitors: 150, Anchor: anchor}).Visits(from, to)) { t.Fatal("not deterministic") }
	for i := 1; i < len(all); i++ { if all[i].Start.Before(all[i-1].Start) { t.Fatal("not in start order") } }
}

func TestDemoCoversEveryFeature(t *testing.T) {
	anchor := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	d := NewDemo(DemoConfig{Seed: 1, TZ: time.UTC, Visitors: 150, Anchor: anchor})
	v := d.Visits(anchor.AddDate(0, 0, -DemoWindowDays), anchor.Add(12*time.Hour))
	// assert: >= 9000 visits; all five UTM fields non-empty somewhere; >= 6 countries with City set;
	// device kinds 0..2 present; names Signup, Purchase, "Outbound Link: Click", "File Download", "404",
	// "Form: Submit", "Web Vitals" present; RevenueMicros > 0 only on Purchase; vitals props within caps;
	// some Kind 2 hits with ScrollPct >= 75 on /blog/*; profiles present and stable per VisitorKey;
	// returning VisitorKeys >= 20%; Replay sessions only in last 14 days and <= 60 per window;
	// Clicks only on /, /pricing, /features/*; both DemoHosts used; Language set on every visit.
}
```

  Add a test that the campaign days have more than 2x the median daily visits and that the dip day has fewer than 0.7x. Add `TestDemoRecordingDecodes`: gunzip, then split lines and decode each as JSON. The first events must be a meta event (type 4) and a full snapshot (type 2); timestamps must not decrease and must fall within the visit's span.
- [ ] **Step 2: Run them and see them fail:** `go test ./internal/synth/ -run 'Demo' -count=1`
- [ ] **Step 3: Implement the model** per spec section 5. Keep the tables in `demo_model.go` as plain data. Add `Anchor time.Time` to `DemoConfig`, and update the spec type in the code comment.
- [ ] **Step 4: Implement `DemoRecording`.** Each page is a small templated HTML document (header, nav, hero, a CTA button, and a footer) built as an rrweb serialized node tree with incremental node ids. Add mouse-move (source 1), click (source 2, type 2) and scroll (source 3) incremental events at the hit offsets, and a new meta plus full snapshot per pageview.
- [ ] **Step 5: Run the tests and see them pass,** then `go test ./tools/gen/ ./internal/synth/ -count=1`, which keeps the fixtures unchanged.

---

### Task 123: Demo service: create, seed history, resume

**Files:**
- Create: `internal/demo/service.go`, `internal/demo/pipeline.go`, `internal/demo/seed.go`, `internal/demo/seed_test.go`
- Read for reference: `cmd/supermetry/load.go` (private writer + session store pipeline, dictionary use, partition creation), `internal/closer/close.go`, `internal/platform/sitesvc/sites.go` (`Create`: authz, audit, registry invalidate), `cmd/supermetry/serve.go:787` (`eraseDeletedSites`, the erase calls)

**Interfaces:**
- Consumes: Task 121's store functions and `Site.Demo`; Task 122's `synth.NewDemo`, `Visits` and `DemoRecording`.
- Produces:

```go
type Deps struct {
	Pool     *pgxpool.Pool
	Registry interface{ Invalidate(); sites.Registry } // sitesvc registry
	Dict     *dict.Dict
	Authz    func(ctx context.Context, p authz.Principal, orgID int64) error // checks ActionSiteCreate
	Audit    func(ctx context.Context, tx pgx.Tx, p authz.Principal, orgID int64, siteID int32) error
	Blobs    replay.BlobStore
	Index    *replay.IndexWriter // or the interface Start/Chunk use
	Now      func() time.Time
	Seed     int64 // 0 = derive from site id
	Visitors float64 // 0 = 150
	Window   int     // days, 0 = synth.DemoWindowDays (tests use 3)
}
type Service struct{ /* … */ }
func New(d Deps) *Service
func (s *Service) Create(ctx context.Context, p authz.Principal, orgID int64, tz string) (store.Site, error) // returns store.ErrDemoExists
func (s *Service) SeedNow(ctx context.Context, siteID int32) error   // synchronous; used by Create's goroutine and tests
func (s *Service) ResumeUnseeded(ctx context.Context) error        // erase + reseed demo sites with demo_seeded_at NULL
func (s *Service) Wait()                                            // waits for background seeds (tests, shutdown)
// pipeline.go: shared by seed and live gap fill
func (s *Service) writeHistory(ctx context.Context, site sites.Site, visits []synth.DemoVisit) error
```

- **Invalid timezone:** `Create` returns `ErrInvalidTimezone`; an empty value means `UTC`.
- **Seeding:** each seed runs `writeHistory` (private `session.NewMemory`, private `writer.New`, writer tracking policy with a one-site registry, then flush/close). It then runs `closer.CloseDay` for each past local day, then `examples` (a no-op until Task 124), then `SetDemoSeeded` and `SetDemoLiveThrough(now)`.
- **Field mapping:**
  - `VisitorID` = `int64(VisitorKey &^ (1<<63))`.
  - Every visit gets its own synthetic session via the private store's `Touch`.
  - Dimensions are interned via `Dict.IDsForSite`, with the kinds from spec section 5.
  - `Screen` comes from `ScreenW`, and `ScreenBucket` is computed the way ingest does.
  - `Web Vitals` hits carry `JoinOnly=true`.

- [ ] **Step 1: Write failing integration tests** (testpg). Build `Deps` with a real pool, a sitesvc registry and the dictionary, and set `Window: 3, Visitors: 40`.
  - `TestSeedFillsEveryFeature`: create an org, `Create`, then `Wait`. Assert `demo_seeded_at` is set. Then, through the query package functions used by the HTTP handlers (find them under `internal/query`), assert non-empty results for:
    - summary and timeseries;
    - breakdowns by page, entry, exit, source, channel, utm_source, utm_medium, utm_campaign, utm_content, utm_term, country, region, city, device, browser, browser_version, os, os_version, language, hostname and screen;
    - custom events and props;
    - revenue (total > 0);
    - vitals, journeys, retention, the sessions list and the profiles list.

    Run it once over a closed day range and once with today included, so it covers both the hot and closed tiers.
  - `TestSeedKathmandu`: timezone `Asia/Kathmandu`. Every closed day appears in `day_close` as done. The summary's day buckets equal the local dates.
  - `TestResumeUnseededReseeds`: insert a demo site with `demo_seeded_at` NULL and some junk events, call `ResumeUnseeded`, then check the event count equals a fresh seed with the same seed/window, and that no session is duplicated.
  - `TestCreateTwiceConflicts`: the second `Create` returns `store.ErrDemoExists`.
- [ ] **Step 2: Run them and see them fail:** `go test ./internal/demo/ -count=1`
- [ ] **Step 3: Implement** `service.go`, `pipeline.go` and `seed.go`. Background seeds use a context derived from a service-level context set by `New` (canceled by `Close()`; add `func (s *Service) Close()`), never the request context.
- [ ] **Step 4: Run the tests and see them pass,** then run `go test ./...`.

---

### Task 124: Demo heatmaps, replays, example settings

**Files:**
- Create: `internal/demo/examples.go`, `internal/demo/examples_test.go`, `internal/platform/store/resources.go`, `internal/platform/store/resources_test.go`
- Modify: `internal/demo/pipeline.go` (heatmaps and replay in `writeHistory`), `internal/platform/http/handlers_resources.go`, `handlers_segments.go`, `handlers_annotations.go` (call the new store functions instead of the inline insert SQL; keep behavior and responses identical)
- Create: `internal/db/migrations/0042_alert_draft_channels.sql`: drop the `channel_ids` cardinality check from `0009_alerts.sql` (find its constraint name in the catalog) and add `CHECK (NOT enabled OR cardinality(channel_ids) > 0)`. Forward-only, transactional.
- Modify: `internal/alerts/service.go` (require at least one channel only when the alert is enabled, on create **and** update/enable; a disabled alert may have zero channels), with tests in the existing alerts test file: a disabled alert with no channels is accepted; enabling it without channels is rejected with the existing validation error; an enabled alert without channels is still rejected.
- Modify: `internal/demo/service.go` (`Deps` gains `Alerts` (the `alerts.Service` or a narrow interface with `CreateAlert`) and `Definitions interface{ Invalidate() }`, the goals/funnels definitions cache the handlers invalidate), `cmd/supermetry/serve.go` only if `demo.New` is already wired there (otherwise Task 125 wires it).

**Interfaces:**
- Produces:
  - `store.InsertGoal(ctx, q, siteID int32, g store.GoalRow) (int64, error)`
  - `store.InsertFunnel(ctx, q, siteID int32, name string, steps json.RawMessage) (int64, error)`
  - `store.InsertAnnotation(ctx, q, siteID int32, day time.Time, text string, authorID int64) (int64, error)`
  - `store.InsertSegment(ctx, q, siteID int32, name string, filters json.RawMessage, authorID int64) (int64, error)`

  The row fields mirror the current inline SQL columns exactly, so read the handlers first.
- `func (s *Service) createExamples(ctx context.Context, site sites.Site, ownerID int64) error`: the settings in spec section 6.1. The alert is created through `alerts.Service.CreateAlert` with `Enabled: false` and no channels. Call the funnel/goal definitions cache invalidation that the handlers call.
- **Heatmaps:** for each pageview hit with `Clicks`, call `EnqueueHeatmap(HeatmapEvent{SiteID, LocalDay, PathID, ViewportWidth: ScreenW, Clicks})` on the private writer.
- **Replays:**
  - For `Replay` visits: `Blobs.Append(ctx, replay.SessionKey(siteID, localDay, sessionID), synth.DemoRecording(v))`.
  - Then `Index.Add(IndexUpdate{…})` with `Page` set to the first path, `Trigger` set to the existing default trigger value, and `ProfileID` taken from the profile, if any.

- [ ] **Step 1: Write the failing tests.**
  - The store functions round-trip.
  - The existing handler tests for goals, funnels, annotations and segments still pass unchanged.
  - Extend `TestSeedFillsEveryFeature`, or add `TestSeedExamples`, so that after a seed:
    - the goals list has 4 entries, each with conversions > 0 (the scroll goal included);
    - the funnel query returns 4 steps with non-zero counts;
    - there are 2 annotations, 2 segments, and 1 alert with `enabled=false` and 0 channels;
    - `heatmap_cells` has rows for `/` and `/pricing`;
    - `replay_sessions` has rows, and each blob opens and decodes (gzip, then NDJSON, with a full snapshot present).
- [ ] **Step 2: Run them and see them fail:** `go test ./internal/demo/ ./internal/platform/store/ ./internal/platform/http/ -count=1`
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run `go test ./...` and see it pass.**

---

### Task 125: Live simulator job and serve wiring

**Files:**
- Create: `internal/demo/live.go`, `internal/demo/live_test.go`
- Modify: `cmd/supermetry/serve.go` (construct `demo.Service` with the shared writer, session store, blobs, index, dictionary and registry; call `ResumeUnseeded` at startup in the background; append the scheduler job; `Close` on shutdown before the writer closes), and the dashboard query handlers' common site-resolution path in `internal/platform/http` (call `demo.Service.Viewed(siteID)` when `site.Demo`)

**Interfaces:**
- Consumes: Task 123's `writeHistory` and the closer usage; the shared `session.Store` (`Touch`, `RecordActivity` via `session.ActivityRecorder`), the shared `*writer.Writer` (`Enqueue`, `EnqueueHeatmap`), and `replay` blobs/index.
- Produces:
  - `func (s *Service) Tick(ctx context.Context) error`: one simulator step for every active demo.
  - `func (s *Service) Job() scheduler.Job`, which returns `{Name: "demo-live", Every: 15 * time.Second, Run: s.Tick}`.
  - `func (s *Service) Viewed(siteID int32)`: throttled at 5 minutes per site in memory, then `store.TouchDemoViewed`.
  - `Deps` gains `Sessions session.Store`, `Writer *writer.Writer` and `Activity session.ActivityRecorder` (if separate).

**Tick algorithm:**

```go
for each st in store.ActiveDemoSites(now-3h):
	from := *st.LiveThrough; to := now
	if to.Sub(from) > 30*time.Minute {
		gapEnd := to.Add(-5 * time.Minute)
		writeHistory(site, demo.Visits(from, gapEnd)); close newly past local days in [from, gapEnd)
		from = gapEnd
	}
	for v in demo.Visits(from, to) (plus carried hits from s.pending[site] with ts <= to):
		emit hits with ts <= to through the shared pipeline: Sessions.Touch(hit) -> event SessionID/VisitorID,
		RecordActivity (skip Kind 2), Writer.Enqueue, EnqueueHeatmap, replay append for Replay visits;
		hits with ts > to go to s.pending[site]
	store.SetDemoLiveThrough(site, to)
```

The synth model must use the same `Anchor` as the seed. Store it as the seed day, which is `created_at`'s local day, so the history and live visits come from one consistent model.

- [ ] **Step 1: Write the failing tests** with a fake `Now`:
  - `TestTickShowsRealtime`: seed (Window 1). Set `demo_viewed_at` to now, advance `Now` by 15 min, run `Tick`. `realtime.RichSnapshot(sessions, siteID, now)` must report visitors > 0, and `demo_live_through` must equal now.
  - `TestTickGapBackfillOnce`: seed, then advance `Now` by 50 h, which crosses at least 2 local days. Run `Tick` twice with no time advance between them. Expected: the event count equals that of a fresh `writeHistory` over the same window, both new past days are closed, and there are no duplicate sessions.
  - `TestTickSkipsUnviewed`: with `demo_viewed_at` older than 3 h, `Tick` writes nothing.
  - `TestViewedThrottled`: two `Viewed` calls within 5 min cause one DB update.
- [ ] **Step 2: Run them and see them fail:** `go test ./internal/demo/ -run 'Tick|Viewed' -count=1`
- [ ] **Step 3: Implement** `live.go` and the serve wiring.
- [ ] **Step 4: Run `go test ./...` and see it pass.**

---

### Task 126: Demo API

**Files:**
- Create: `internal/platform/http/handlers_demo.go`, `internal/platform/http/handlers_demo_test.go`
- Modify: `internal/platform/http/private.go` (routes and deps: the `Demo` service interface), `handlers_account.go` (`h.me` adds `demo_prompt_dismissed`), `handlers_sites.go` (site JSON adds `is_demo` and `demo_ready`; `PATCH` ignores `is_demo`), `internal/api/openapi.yaml`, `cmd/supermetry/serve.go` (pass the service)

**Interfaces:**
- Consumes: Task 123's `demo.Service.Create`, `store.ErrDemoExists`, `demo.ErrInvalidTimezone`, `store.DismissDemoPrompt` and `store.DemoPromptDismissed`.
- Produces the HTTP contract (spec section 8):
  - `POST /api/private/orgs/{org}/demo`, with body `{"timezone": string}`:
    - 202 returns the site JSON;
    - 409 returns `{"error":"demo_exists","site_id":<public id>}`;
    - 400 returns `invalid_timezone`;
    - 403 per authz.
  - `POST /api/private/me/demo-prompt/dismiss` returns 204 and is idempotent.
  - `GET /api/private/me` adds `"demo_prompt_dismissed": bool`.
  - Site objects add `"is_demo": bool, "demo_ready": bool`.
  - The dashboard handlers call `Viewed` (wired in Task 125).

- [ ] **Step 1: Write failing handler tests** following the existing handler test helpers in `internal/platform/http`:
  - create returns 202, and a second create returns 409 with the same id;
  - a viewer-role member gets 403;
  - a bad timezone gets 400;
  - dismiss returns 204, and `me` then shows `true`;
  - the site list shows `is_demo` and `demo_ready`;
  - `PATCH` with `is_demo:false` leaves it true.
- [ ] **Step 2: Run them and see them fail:** `go test ./internal/platform/http/ -run Demo -count=1`
- [ ] **Step 3: Implement the handlers and the OpenAPI schema changes.**
- [ ] **Step 4: Run `go test ./...` and see it pass.**

---

### Task 221: Demo UI

**Files:**
- Create: `web/src/features/demo/api.ts`, `useDemo.ts`, `DemoChoice.tsx`, `DemoBanner.tsx`, `RemoveDemoBanner.tsx`, and a test file for each component
- Modify: `web/src/routes/app.index.tsx` (onboarding), `web/src/components/Sidebar.tsx` (switcher item and badge), the site dashboard layout route `web/src/routes/app.$org.$site.tsx` (or whichever layout wraps the dashboard pages; mount `DemoBanner` and `RemoveDemoBanner`), the site settings tracking section (hide install for demo), `web/src/features/sites/api.ts` (types), `web/src/lib/api.gen.ts` (regenerate with `bun run gen:api`)

**Interfaces:**
- Consumes: Task 126's HTTP contract.
- Produces UI behavior per spec section 9.
  - `useCreateDemo(org)` posts `Intl.DateTimeFormat().resolvedOptions().timeZone`. On 202 or 409 it navigates to `/app/$org/<site public id>`.
  - `useDismissDemoPrompt()`.
  - The site query polls every 2 s while `demo_ready` is false.

- [ ] **Step 1: Write failing component tests** (Vitest + Testing Library, following the existing `*.test.tsx` patterns):
  - **Onboarding:** with no sites and `demo_prompt_dismissed=false`, it shows "Explore with demo data". Clicking it calls create and navigates. "Skip for now" calls dismiss. With `demo_prompt_dismissed=true` the demo choice is hidden.
  - **Switcher:** "Add demo project" appears when the org has no demo and is hidden when it has one. The demo row has a "Demo" badge.
  - **DemoBanner:** shows "Generating demo data…" while `demo_ready` is false and "You're viewing demo data" afterwards. Remove calls `deleteSite` after a confirm dialog.
  - **RemoveDemoBanner:** shown when the org has a demo and a real site. Keep hides it and persists it in localStorage; a throwing localStorage still renders correctly.
  - **Settings:** a demo site shows "Demo projects don't accept real traffic." and no snippet.
- [ ] **Step 2: Run them and see them fail:** `cd web && bun run test --run src/features/demo`
- [ ] **Step 3: Implement** with shadcn components that are already in the project (Button, Badge, Alert, Dialog, DropdownMenuItem). Match the existing copy style: sentence case, no exclamation marks.
- [ ] **Step 4: Run `cd web && bun run typecheck && bun run test --run && bun run build` and see it pass.**

---

### Task 127: Review fixes

Findings from the branch review (2026-10-10). Fix each one test-first. File lists are where the defect is. Touch other files only if a fix needs them.

**Files:** `internal/demo/seed.go`, `internal/demo/live.go`, `internal/demo/seed_test.go`, `internal/demo/live_test.go`, `internal/demo/service.go`, `cmd/supermetry/serve.go`, `internal/replay/css_handler.go` (or the CSS lookup closure in `serve.go`), `internal/replay/handler.go` (`CollectionGuard`), `internal/platform/http/handlers_alerts.go`, `internal/alerts/service.go`, `internal/platform/http/handlers_sites.go`, `internal/retention/*.go` (only if profiles erase belongs there), `web/src/features/demo/RemoveDemoBanner.tsx` and its test, plus the matching test files.

- [ ] **1. Seed failures are visible and retried.**
  - **Bug:** `Create` drops the error from `seedOnce` (`seed.go:67`).
  - **Fix:** log it with `slog.Error`, giving the site id and the error, never an IP or UA. Retry up to 3 times with a backoff of 2 s, then 10 s, then 30 s, and stop if `s.ctx` is cancelled.
  - **After the last failure:** set `sites.settings.demo_seed_failed_at`. The site JSON then reports `demo_failed: true`. `DemoBanner` shows "Demo data could not be generated." with a "Remove demo project" action, and stops polling.
  - **Test:** inject a failing step through a test hook in `Deps`, then assert the retry count and the failed flag.
- [ ] **2. Resume and Create never race.**
  - **Fix:** `ResumeUnseeded` skips site ids present in `s.running`. Erase plus seed runs under the same per-site `running` guard that `SeedNow` uses. `ErrDemoAlreadySeeded` makes it `continue`, not return.
  - **Test:** start a seed (blocked by a hook), call `ResumeUnseeded`, release the hook. The data must equal one clean seed, and no erase may happen.
- [ ] **3. Replay CSS and `CollectionGuard` treat a demo site as unknown.**
  - **Test:** a CSS upload for a demo `public_id` gets the unknown-site response, and no blob is written.
- [ ] **4. A live tick never duplicates hits.**
  - **Fix:** advance the per-site pending cursor as each hit is enqueued, so an error after a partial emit doesn't re-emit what was already sent.
  - **Test:** fail `SetDemoLiveThrough` once through a hook, run `Tick` twice, and assert the event count equals that of a clean run.
- [ ] **5. One site's tick error doesn't block the others.** `Tick` logs the per-site error, continues with the remaining sites, and returns a joined error at the end.
  - **Test:** two demo sites, the first failing; the second still advances.
- [ ] **6. Disabled draft alerts work through HTTP too.**
  - **Fix:** `validAlertInput` takes `enabled` into account, matching the service: channels are required only when the alert is enabled. Normalize nil `ChannelIDs` to an empty slice before the insert or update, so it never becomes SQL NULL.
  - **Tests:**
    - POST of a disabled alert with no channels returns 2xx;
    - PATCH that enables it without channels returns the existing 4xx validation error;
    - PATCH of the demo alert's threshold while it is disabled succeeds.
- [ ] **7. `is_demo`, `demo_ready` and `demo_failed` appear only on `/api/private` responses**, not on `/api/public/v1`.
  - **Test:** a public site GET doesn't contain those keys.
- [ ] **8. The `RemoveDemoBanner` "keep" key includes the demo site's public id,** so removing and re-adding the demo shows the banner again.
  - **Test:** keep demo A; then demo B (new id) shows the banner.
- [ ] **9. Profiles are fresh after a resume.** The resume erase also deletes the demo site's `profiles` rows (and `profile_tombstones`, if present).
  - **Test:** after a resume, `first_seen_at` of every profile is at or after the reseed's window start, and the count equals a clean seed.
- [ ] **10. Live sessions split like seeded ones.** The live path ends a visit's session the way the seed does (`Expire`), so two visits by the same `VisitorKey` inside the idle timeout stay two sessions.
  - **Test:** the session count after live ticks equals the count from `writeHistory` over the same window.
- [ ] **11. No owner means no examples, not a broken seed.** If `demoOwner` finds no owner, skip the annotations and segments that need an author, and still create the rest.
  - **Test:** an org whose owner was removed still seeds successfully.
- [ ] **12. Deleting a demo clears its pending visits.** When `Tick` finds that a pending site is no longer an active demo, it drops that site's `s.pending` entry.
  - **Test:** delete the site, run `Tick`, and the pending map is empty.
- [ ] **13. Missing tests.**
  - **(a) Hot tier:** `TestSeedFillsEveryFeature` adds a query over **today only**, before any close, asserting summary, pages, sources and custom events are non-empty.
  - **(b) Day-crossing time zone:** a seed in `Asia/Kolkata` with `Now` at 20:00 UTC, so the local date is a day ahead. Assert `day_close` dates and summary day buckets use local dates.
  - **(c) Deletion:** after the site delete plus the `eraseDeletedSites` path, the demo site has no rows in events, sessions, replay_sessions, heatmap_cells or profiles.
  - **(d) Mid-seed interruption:** cancel the seed context halfway through `writeHistory`, then call `ResumeUnseeded`. The result must equal one clean seed.

Run `go test ./...` and the web gate, and see both pass.

---

### Task 128: Busy traffic while watched, plus model variety

Spec: section 11, amendments 1 (revised) and 2.

**Note:** an earlier, aborted attempt left partial changes in the tree:
- **Keep:** the expanded variety tables in `internal/synth/demo_model.go`, if they are correct.
- **Remove:** the `DemoConfig.BusyFrom` approach. Busy traffic is no longer part of `Visits`, and `Visits` must be unchanged by this task apart from the variety tables.

**Files:**
- Modify: `internal/synth/demo.go`, `internal/synth/demo_model.go`, `internal/demo/live.go`, `internal/demo/service.go`, `internal/platform/http/private.go` (route and `Demo` interface), `internal/platform/http/handlers_demo.go`, `internal/api/openapi.yaml`, `web/src/features/demo/DemoBanner.tsx` (or a new `useDemoHeartbeat.ts`), `web/src/features/demo/api.ts`, `web/src/lib/api.gen.ts` (`bun run gen:api`)
- Test: `internal/synth/demo_test.go`, `internal/demo/live_test.go`, `internal/platform/http/handlers_demo_test.go`, `web/src/features/demo/*.test.tsx`

**Interfaces:**
- synth:
  - `func (d *Demo) BusyVisit(start time.Time, key uint64) DemoVisit` returns one busy session starting at `start`. It is deterministic per `(Seed, key)`: 8–25 min, a pageview every 40–150 s, engagement hits, the usual custom event, revenue and vitals odds, `Replay` for 2% of keys, and `Clicks` like the model. The key uses bit 62.
  - `func BusyTarget(local time.Time) int` returns `10 + round(10*shape(localHour))`, where `shape` is the diurnal curve normalized to 0..1 (lowest around 03:00, peak in the afternoon).
- demo:
  - `func (s *Service) Heartbeat(siteID int32)` records `s.watched[siteID] = Now()`. It also calls `Viewed(siteID)`.
  - `func (s *Service) watching(siteID int32, now time.Time) bool` returns `now - watched[siteID] <= 60s`.
  - Add a constant `heartbeatTTL = 60 * time.Second`.
  - Prune `watched` entries older than 10 min on each `Tick`.
- HTTP: `POST /api/private/sites/{site}/demo/heartbeat` returns 204 for a demo site the principal can see (through `visibleSite`) and 404 for any non-demo site. It requires a logged-in session; it is not under the public API or API keys.
- **Tick, per active demo, after the quiet live emit:**
  - If `watching`:
    1. `active := Sessions.ActiveSince(siteID, now - realtime.LiveWindow)`.
    2. `need := BusyTarget(now.In(TZ)) - active`.
    3. For `need > 0`, create `need` busy visits with deterministic keys from `(siteID, now.Unix()/15, i)`. Their start times are `now - U(1m, 15m)` on the first watched tick (warm start) and `now - U(0, 15s)` afterwards.
    4. Emit their hits with `ts <= now` through the shared pipeline (`Touch`, `RecordActivity`, `Writer.Enqueue`, heatmaps, replay). Later hits go to `s.busyPending[siteID]`.
  - If not watching: clear `s.busyPending[siteID]`.
  - Busy traffic never runs in gap fill or seeding.
- **Web:** while a demo dashboard route is mounted, send the heartbeat right away, every 30 s while the document is visible, and on `visibilitychange` to visible. Stop on hidden and on unmount. Ignore network errors.

- [ ] **Step 1: Write failing tests.**
  - `TestBusyVisitShape` (synth): it is deterministic, the duration is 8–25 min, the pageview gaps are within 40–150 s, the fields are populated, and the key has bit 62.
  - `TestBusyTargetRange`: the value is in [10, 20] for all 24 hours, with 15:00 above 03:00.
  - `TestVisitsUnchangedByBusy`: `Visits` contains no bit-62 keys.
  - `TestDemoVariety`: over 7 days, at least 35 countries, at least 100 cities, at least 8 browsers, at least 6 OSes, at least 3 device kinds, at least 15 languages, at least 25 referrer hosts, at least 6 UTM campaigns and at least 30 paths. Language/country and browser/OS consistency must hold.
  - `TestTickBusyWhileWatched` (demo): seed with a fixed `Now` in `Asia/Kolkata` at 03:00 local, `Heartbeat`, then `Tick`.
    - `realtime.RichSnapshot` visitors must be in [10, 24] right after the first tick, because of the warm start.
    - After the writer flushes, events with `ts > now-5m` must exist in the DB (poll with a deadline).
    - Then 6 ticks at 15 s steps, with a heartbeat every 30 s: the visitors stay in [10, 24], and `events` has no duplicate `(session_id, ts, kind)`.
  - `TestTickBusyStopsWhenNotWatched`: after the heartbeat is older than 60 s, `Tick` emits no busy hits and `busyPending` is empty. 5 minutes later, realtime visitors drop below 10.
  - **Handler:** heartbeat returns 204 for a demo, 404 for a real site, and 404 for a site in another org.
  - **Web:**
    - It sends on mount and every 30 s while visible.
    - It doesn't send while hidden.
    - It resumes on visible.
    - It stops on unmount.
- [ ] **Step 2: Run them and see them fail.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run `go test ./...` and the web gate, and see both pass.** Run the new Go tests with `-count=5`.

---

### Task 129: 24-hour demo lifetime

Spec: section 11, amendment 3.

**Files:**
- Create: `internal/db/migrations/0043_demo_started_at.sql`
- Modify: `internal/platform/store/sites.go` (`CreateDemoSite` sets `demo_started_at = now()`; new funcs), `internal/demo/service.go`, `internal/demo/expiry.go` (new), `cmd/supermetry/serve.go` (register the job), `internal/platform/http/handlers_sites.go` (private response adds `demo_expires_at`), `internal/api/openapi.yaml`, `web/src/features/demo/DemoBanner.tsx` and its test, `web/src/features/sites/api.ts`, `web/src/lib/api.gen.ts` (`bun run gen:api`)
- Test: `internal/demo/expiry_test.go`, `internal/platform/store/sites_demo_test.go`, `internal/platform/http/handlers_demo_test.go`, `web/src/features/demo/DemoBanner.test.tsx`

**Migration `0042`:**

```sql
ALTER TABLE sites ADD COLUMN demo_started_at timestamptz;
UPDATE sites SET demo_started_at = coalesce(demo_seeded_at, now()) WHERE is_demo AND demo_started_at IS NULL;
```

**Interfaces:**
- `const DemoLifetime = 24 * time.Hour` (in `internal/demo`).
- `store.ExpiredDemoSites(ctx, q, startedBefore time.Time) ([]store.ExpiredDemo, error)` returns `{SiteID int32; OrgID int64}` for live (not deleted) demos.
- `func (s *Service) ExpireDue(ctx context.Context) error`: for each expired demo, soft-delete it through the same store call and audit path as a user delete. The audit uses a system principal, or audit `nil` if that's what other system jobs do; follow the existing system-job pattern. Then invalidate the registry and drop the site's `s.pending`. Erasure then happens through the existing `site-erasure` job.
- `func (s *Service) ExpiryJob() scheduler.Job`: `{Name: "demo-expiry", Every: time.Minute, Run: s.ExpireDue}`, registered in `serve.go`.
- Private site JSON adds `"demo_expires_at": RFC3339` (`demo_started_at + DemoLifetime`), only on `/api/private`.

**UI:** `DemoBanner` shows "Demo expires in {h}h {m}m" (in hours, or minutes when under 1 h), next to "Remove demo project". When the expiry time has passed, the site query returns 404: redirect to `/app/$org` and invalidate the sites list. "Add demo project" works again after expiry.

- [ ] **Step 1: Write failing tests.**
  - **Store:** `demo_started_at` is set on create; `ExpiredDemoSites` returns only demos started before the cutoff, never real sites or already-deleted demos.
  - `TestExpireDueDeletesAndErases` (testpg): create and seed a demo with a fake `Now`, advance `Now` by 24 h + 1 min, call `ExpireDue`. Expected: the site has `deleted_at` set and an audit `site.deleted` row. After the `eraseDeletedSites` path, the site has no rows in events, sessions, replay_sessions, heatmap_cells or profiles. A new demo can then be created for the org.
  - `TestExpireDueKeepsYoungDemo`: at 23 h 59 m the demo survives.
  - **Handler:** private site JSON has `demo_expires_at` equal to start + 24 h; the public API doesn't.
  - **Web:** the banner shows the countdown text for a fixed clock; after expiry, a 404 navigates to the org home.
- [ ] **Step 2: Run them and see them fail.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run `go test ./...` and the web gate, and see both pass.**

---

### Task 130: Review fixes for Tasks 128–129

Each fix is test-first.

**Files:** `web/src/features/demo/DemoBanner.tsx` (and its heartbeat hook, if separate) with its test, `internal/db/migrations/0044_demo_started_default.sql` (new), `internal/platform/store/sites.go`, `internal/demo/expiry.go`, `internal/demo/seed.go`, `internal/demo/live.go`, and the matching `_test.go` files.

**Migration numbering:** the `org-lifecycle` branch also adds a migration, and will renumber its own on merge. Use `0044_demo_started_default.sql` here.

- [ ] **1. Expired demo in an open tab.**
  - When `Date.now() >= expires_at`, invalidate and refetch the site query (or `me`), so the existing 404 path redirects to `/app/$org`.
  - Stop the heartbeat interval when a heartbeat returns 404.
  - **Test (fake timers):** past expiry, the site is refetched and the redirect happens. A 404 heartbeat stops further heartbeats.
- [ ] **2. Rolling-deploy NULL `demo_started_at`.** Add migration `0044_demo_started_default.sql`:
  - `ALTER TABLE sites ALTER COLUMN demo_started_at SET DEFAULT now();`
  - `UPDATE sites SET demo_started_at = coalesce(demo_seeded_at, now()) WHERE is_demo AND demo_started_at IS NULL;`
  - Also make `ExpiredDemoSites` and the API's `demo_expires_at` use `coalesce(demo_started_at, created_at)`.
  - **Test:** a demo row with a NULL `demo_started_at` still expires, and still reports an expiry.
- [ ] **3. Expiry versus an in-flight seed.**
  - `ExpireDue` skips site ids present in `s.running`; they are retried on the next run.
  - In the seed, an `ErrNoRows` (or `pgx.ErrNoRows`) from the demo timestamp updates means the site was deleted: stop immediately, with no retry and no `erasePartial`.
  - **Test:** a seed blocked by a hook; `ExpireDue` leaves that site alone. The site deleted mid-seed makes the seed end without a retry.
- [ ] **4. Busy key reuse within one 15 s bucket.**
  - Keep a per-site monotonic counter in `Service` (`busySeq map[int32]uint64`), and build busy keys from `(siteID, busySeq++)` instead of `(siteID, bucket, i)`. Prune it with the other per-site maps.
  - **Test:** two ticks within the same 15 s bucket produce distinct busy visitor keys, and no duplicate `(session_id, ts, kind)` rows.
- [ ] **5. Code comment** in `live.go` above `watched`: demo realtime and busy traffic assume the single scheduler instance; heartbeats that land on another instance are not seen.

Run `go test ./...` and the web gate, and see both pass.
