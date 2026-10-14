# Pixel Analytics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "Pixel analytics" project type (image pixels and view-count badges for READMEs and docs), branded linked badges, and an opt-in open counter service for GitHub keys.

**Architecture:** `sites.kind` splits projects into `web` and `pixel`. A new `internal/pixels` package owns the pixel registry, an in-memory all-time `TotalCache`, proxy detection, a buffered `Recorder` that flushes daily aggregates, the GIF/SVG handlers and the SVG badge renderer. A sibling `internal/opencounters` package reuses the renderer, cache and proxy detection for `/c/github.com/...`, behind `SUPERMETRY_OPEN_COUNTERS`. Private API under `/api/private/sites/{site}/pixels`, documented in `internal/api/openapi.yaml`; the React dashboard branches on `kind`.

**Tech Stack:** Go (`net/http`, `pgx/v5`, `slog`), PostgreSQL, Bun + Vite + React + TanStack Router/Query + shadcn/ui, Playwright.

**Spec:** `docs/superpowers/specs/2026-10-14-pixel-analytics-design.md`

## Global Constraints

- No DB read on the request hot path: pixel and counter lookups, totals and limits are in memory.
- Daily aggregates only. Never store raw IPs, user agents, or (for open counters) countries.
- Forward-only migrations: `0160_pixel_projects.sql`, `0170_open_counters.sql` (steps of 10).
- `sites.kind text NOT NULL DEFAULT 'web' CHECK (kind IN ('web','pixel'))`; kind is immutable after creation.
- Pixel `public_id` is 12 characters and starts with `p`; `name` 1–120 chars; `badge_label` default `'views'`, max 40 chars.
- `pixel_views_daily` primary key `(pixel_id, day, country, via)`; `via IN ('proxy','direct')`; `country = ''` for proxied views.
- Proxy allowlist by UA: `github-camo` (name `camo`), `GoogleImageProxy` (name `google`). Proxy limiter 20/s, burst 200, keyed by `pixel id + proxy name`.
- Image endpoints never return an error status; `Cache-Control: no-store, max-age=0`.
- 100 pixels per project; the 101st returns `409 application/problem+json`.
- `SUPERMETRY_BADGE_BRANDING` default `true`. `?style=flat|flat-square`, `?color=<hex>` validated with fallback. Badge height 20 px.
- Linked snippet: `[![views](BASE/px/ID.svg)](HOMEPAGE/?ref=badge)` with `HOMEPAGE = SUPERMETRY_HOMEPAGE_URL`.
- `SUPERMETRY_OPEN_COUNTERS` default `false`; routes 404 when off. Owner `[A-Za-z0-9-]{1,39}`, repo `[A-Za-z0-9._-]{1,100}`, lower-cased. Only proxied views count. New-counter cap 50/min burst 500. Delete counters idle 365 days and daily rows older than 400 days.
- Existing `pixel_impressions_daily` and `/px/<site public id>.gif?c=` keep working unchanged.
- UI copy says "views, not unique visitors".
- Licensing: no code from AGPL/EUPL/proprietary reference repos; badge layout is our own (shields.io idea only).
- Go tests that need Postgres use `testpg.New(t)` + `db.Migrate` (needs `SUPERMETRY_TEST_DSN`; `make test` sets it). Frontend: `cd web && bun run test --run`, `bun run typecheck`.
- Commits: identity `velixio <270455167+velixio@users.noreply.github.com>`, message describes the change only, no trailers. Every commit must pass `go build ./... && go vet ./...` (and `bun run typecheck` once web files change).

## Review Focus

1. **Label/key injection into SVG:** a badge label or open-counter key containing `<`, `&`, `"` must be escaped; the SVG stays well-formed (Task 6 test `TestRenderEscapesLabel`, Task 10 test via key regex).
2. **Odd pixel file names:** `/px/pABC.svg.gif`, `/px/.svg`, 200-char IDs, `HEAD` requests: always an image, never a count, never a panic (Task 7 test `TestOddFileNamesServeImageWithoutCounting`).
3. **Site public ID that starts with `p`:** existing 12-char site IDs like `pubsite00001` must still route to the web pixel path (Task 7 test `TestWebPixelStillServedForSiteIDStartingWithP`).
4. **Flush failure:** the total cache must not double-count or lose counts when a flush fails and is retried (Task 5 test `TestFlushFailureKeepsCountsAndTotals`).
5. **Huge totals / formatting edges:** 999, 1000, 999_950 (must render `1M`, not `1000k`), 1_234_567_890 (Task 6 test `TestFormatCount`).

## File Structure

| Path | Responsibility |
|---|---|
| `internal/db/migrations/0160_pixel_projects.sql` | `sites.kind`, `pixels`, `pixel_views_daily` |
| `internal/db/migrations/0170_open_counters.sql` | `open_counters`, `open_counter_views_daily` |
| `internal/sites/registry.go` | `Site.Kind` |
| `internal/platform/store/sites.go`, `internal/platform/sitesvc/{sites,registry}.go` | persist / load kind |
| `internal/pixels/store.go` | pixel CRUD, stats, totals query |
| `internal/pixels/registry.go` | in-memory `public_id → Pixel`, reload |
| `internal/pixels/totals.go` | `TotalCache` |
| `internal/pixels/proxy.go` | `DetectProxy`, `ProxyLimiter` |
| `internal/pixels/recorder.go` | buffered `pixel_views_daily` writes |
| `internal/pixels/badge.go` | `FormatCount`, `RenderBadge` |
| `internal/pixels/handler.go` | `/px/{file}` for pixel IDs, delegates others to links |
| `internal/platform/http/handlers_pixels.go` | private CRUD + stats |
| `internal/opencounters/*` | key parsing, recorder, handler, stats page, cleanup |
| `internal/config/config.go` | `BadgeBranding`, `OpenCounters`, `OpenCounterBlocklist` |
| `web/src/features/pixels/*` | api hooks, snippets, dashboard components |
| `web/src/features/sites/Onboarding.tsx` | kind choice |

---

### Task 1: Migration 0160 (kind, pixels, pixel_views_daily)

**Files:**
- Create: `internal/db/migrations/0160_pixel_projects.sql`
- Test: `internal/db/migrate_pixel_projects_test.go`

**Interfaces:**
- Produces: tables `pixels(id, site_id, public_id, name, badge_label, enabled, created_at)`, `pixel_views_daily(pixel_id, day, country, via, views)`, column `sites.kind`.

- [ ] **Step 1: Write the failing test**

```go
package db_test

import (
	"context"
	"testing"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestPixelProjectsMigration(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Px','px') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	var kind string
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'pxsite000001','S','UTC',30) returning id, kind`, orgID).Scan(&siteID, &kind); err != nil {
		t.Fatal(err)
	}
	if kind != "web" {
		t.Fatalf("default kind = %q, want web", kind)
	}
	if _, err := pool.Exec(ctx, `update sites set kind='email' where id=$1`, siteID); err == nil {
		t.Fatal("kind check accepted 'email'")
	}
	var pixelID int64
	var label string
	if err := pool.QueryRow(ctx, `insert into pixels(site_id,public_id,name) values ($1,'pAbCdEfGhIjK','GitHub profile') returning id, badge_label`, siteID).Scan(&pixelID, &label); err != nil {
		t.Fatal(err)
	}
	if label != "views" {
		t.Fatalf("badge_label default = %q", label)
	}
	for _, bad := range []string{
		`insert into pixels(site_id,public_id,name) values ($1,'short','x')`,
		`insert into pixels(site_id,public_id,name) values ($1,'xAbCdEfGhIjK','x')`,
		`insert into pixels(site_id,public_id,name) values ($1,'pAbCdEfGhIj2','   ')`,
	} {
		if _, err := pool.Exec(ctx, bad, siteID); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
	if _, err := pool.Exec(ctx, `insert into pixel_views_daily(pixel_id,day,country,via,views) values ($1,'2026-10-14','','proxy',3)`, pixelID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pixel_views_daily(pixel_id,day,country,via,views) values ($1,'2026-10-14','','bot',1)`, pixelID); err == nil {
		t.Fatal("via check accepted 'bot'")
	}
	if _, err := pool.Exec(ctx, `delete from pixels where id=$1`, pixelID); err != nil {
		t.Fatal(err)
	}
	var left int
	_ = pool.QueryRow(ctx, `select count(*) from pixel_views_daily`).Scan(&left)
	if left != 0 {
		t.Fatalf("daily rows left after cascade = %d", left)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/db -run TestPixelProjectsMigration -count=1`
Expected: FAIL, `column "kind" does not exist`.

- [ ] **Step 3: Write the migration** (load `.claude/skills/supabase-postgres-best-practices/SKILL.md` first)

```sql
-- Pixel analytics projects: a site is either web analytics or pixel analytics.
-- Pixel views are daily aggregates; no IPs or user agents are stored.

ALTER TABLE sites ADD COLUMN kind text NOT NULL DEFAULT 'web' CHECK (kind IN ('web', 'pixel'));

CREATE TABLE pixels (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    public_id text NOT NULL UNIQUE CHECK (public_id ~ '^p[A-Za-z0-9]{11}$'),
    name text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 120),
    badge_label text NOT NULL DEFAULT 'views' CHECK (char_length(badge_label) BETWEEN 1 AND 40),
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX pixels_site_id_idx ON pixels (site_id, id);

CREATE TABLE pixel_views_daily (
    pixel_id bigint NOT NULL REFERENCES pixels(id) ON DELETE CASCADE,
    day date NOT NULL,
    country text NOT NULL DEFAULT '' CHECK (char_length(country) <= 2),
    via text NOT NULL CHECK (via IN ('proxy', 'direct')),
    views bigint NOT NULL CHECK (views > 0),
    PRIMARY KEY (pixel_id, day, country, via)
);
```

`ADD COLUMN ... DEFAULT` with a constant is metadata-only on PG 11+, so it is safe on large `sites` tables.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/db -count=1`
Expected: PASS (`ok supermetry/internal/db`).

- [ ] **Step 5: Commit**

```bash
git add internal/db/migrations/0160_pixel_projects.sql internal/db/migrate_pixel_projects_test.go
git commit -m "feat(db): add pixel projects migration"
```

### Task 2: Site kind through store, registry and create API

**Files:**
- Modify: `internal/sites/registry.go:12` (add `Kind string` to `Site`)
- Modify: `internal/platform/store/sites.go:89` (`Site.Kind`), `:133` (`CreateSite` insert), `scanSite` and the select lists that feed it (add `kind`)
- Modify: `internal/platform/sitesvc/registry.go` (`coreSite` copies `Kind`)
- Modify: `internal/platform/sitesvc/sites.go:101` (`Create` takes a `CreateInput`)
- Modify: `internal/platform/http/private.go:166` (`CreateSiteRequest.Kind`), `:382`
- Modify: `internal/platform/http/handlers_sites.go` (`responseSite` emits `kind`)
- Modify: `internal/api/openapi.yaml:3975` (`CreateSiteRequest`), the `Site` response schema
- Test: `internal/platform/sitesvc/sites_test.go`, `internal/platform/http/handlers_sites_test.go`

**Interfaces:**
- Produces: `sites.Site.Kind` (`"web"|"pixel"`), constants `sites.KindWeb = "web"`, `sites.KindPixel = "pixel"`; `sitesvc.CreateInput{Name string; Kind string; Hosts []string; Timezone string; RetentionDays int; RevenueCurrency string}`; `(*sitesvc.Sites).Create(ctx, principal, CreateInput) (coresites.Site, error)`; JSON field `kind` on site responses.

- [ ] **Step 1: Write the failing service test** (append to `internal/platform/sitesvc/sites_test.go`, reusing that file's existing fixture that builds `*Sites` and an owner principal; read the top of the file for its helper name before writing)

```go
func TestCreatePixelProjectNeedsNoHostsOrCurrency(t *testing.T) {
	svc, owner := newSitesFixture(t) // existing helper in this file; rename to match
	site, err := svc.Create(context.Background(), owner, CreateInput{
		Name: "Profile views", Kind: coresites.KindPixel, Timezone: "Asia/Kathmandu", RetentionDays: 365,
	})
	if err != nil {
		t.Fatal(err)
	}
	if site.Kind != coresites.KindPixel || len(site.AllowedHosts) != 0 {
		t.Fatalf("site = %+v", site)
	}
	if _, err := svc.Create(context.Background(), owner, CreateInput{Name: "x", Kind: "email", Timezone: "UTC", RetentionDays: 30}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad kind err = %v", err)
	}
	web, err := svc.Create(context.Background(), owner, CreateInput{Name: "w", Hosts: []string{"a.example"}, Timezone: "UTC", RetentionDays: 30, RevenueCurrency: "USD"})
	if err != nil || web.Kind != coresites.KindWeb {
		t.Fatalf("default kind = %+v, %v", web, err)
	}
	if _, err := svc.Create(context.Background(), owner, CreateInput{Name: "w2", Timezone: "UTC", RetentionDays: 30, RevenueCurrency: "USD"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("web without hosts err = %v", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/platform/sitesvc -run TestCreatePixelProject -count=1`
Expected: FAIL to compile, `undefined: CreateInput`.

- [ ] **Step 3: Implement**

In `internal/sites/registry.go`:

```go
const (
	KindWeb   = "web"
	KindPixel = "pixel"
)
```

and add `Kind string // KindWeb or KindPixel; fixed at creation` to `Site`.

In `internal/platform/store/sites.go` add `Kind string` to `Site`, and change the insert:

```go
		insert into sites (org_id, public_id, name, allowed_hosts, timezone, retention_class, revenue_currency, revenue_currency_state, settings, kind)
		values ($1, $2, $3, $4, $5, $6, nullif($7, ''), $8, '{"profiles_enabled": true}'::jsonb, $9)
```

passing `site.RevenueCurrencyState` as `$8` and `site.Kind` as `$9`. Add `kind` to every `select` feeding `scanSite` (grep `scanSite(` for callers) and scan it into `Kind`. In `sitesvc/registry.go`, `coreSite` sets `Kind: row.Kind`.

In `sitesvc/sites.go`:

```go
// CreateInput is a new project. Pixel projects need no hosts or currency.
type CreateInput struct {
	Name            string
	Kind            string
	Hosts           []string
	Timezone        string
	RetentionDays   int
	RevenueCurrency string
}

func (s *Sites) Create(ctx context.Context, principal authz.Principal, in CreateInput) (coresites.Site, error) {
	if !authz.Can(principal, authz.ActionSiteCreate, 0) {
		return coresites.Site{}, ErrForbidden
	}
	if in.Kind == "" {
		in.Kind = coresites.KindWeb
	}
	var (
		name      string
		hosts     []string
		location  *time.Location
		retention int
		err       error
		state     = string(coresites.RevenueCurrencyConfigured)
	)
	switch in.Kind {
	case coresites.KindWeb:
		name, hosts, location, retention, err = validateSite(in.Name, in.Hosts, in.Timezone, in.RetentionDays)
		if err == nil && !coresites.ValidCurrency(in.RevenueCurrency) {
			err = fmt.Errorf("%w: revenue currency must be three uppercase letters", ErrInvalid)
		}
	case coresites.KindPixel:
		name, _, location, retention, err = validateSite(in.Name, []string{"pixel.invalid"}, in.Timezone, in.RetentionDays)
		hosts, in.RevenueCurrency, state = []string{}, "", string(coresites.RevenueCurrencyUnconfigured)
	default:
		err = fmt.Errorf("%w: kind must be web or pixel", ErrInvalid)
	}
	if err != nil {
		return coresites.Site{}, err
	}
	// ... existing transaction body unchanged, with store.Site{..., Kind: in.Kind,
	//     RevenueCurrency: in.RevenueCurrency, RevenueCurrencyState: state}
```

(If `RevenueCurrencyUnconfigured` is spelled differently, grep `RevenueCurrencyState` constants in `internal/sites` and use the "not yet chosen" one.) Update every other caller of `Create` (`grep -rn "Sites.Create(\|\.Create(ctx, principal, request.Name" internal cmd`) to pass a `CreateInput`.

In `private.go`: add `Kind string \`json:"kind"\`` to `CreateSiteRequest`, and `Services.CreateSite` maps the request field-by-field into `sitesvc.CreateInput`. `responseSite` adds `Kind`.

OpenAPI `CreateSiteRequest`:

```yaml
    CreateSiteRequest:
      type: object
      additionalProperties: false
      required: [name, timezone, retention_days]
      description: hosts and revenue_currency are required when kind is web (the default) and ignored for pixel.
      properties:
        name: { type: string }
        kind: { type: string, enum: [web, pixel], default: web, description: Fixed at creation. }
        hosts:
          type: array
          items: { type: string }
        timezone: { type: string }
        retention_days: { type: integer, enum: [0, 7, 30, 90, 365] }
        revenue_currency: { type: string, pattern: '^[A-Z]{3}$' }
```

Add `kind: { type: string, enum: [web, pixel] }` to the site response schema (find it with `grep -n "ManagedSite:\|  Site:" internal/api/openapi.yaml`) and to its `required` list.

- [ ] **Step 4: Handler test** (append to `internal/platform/http/handlers_sites_test.go`, using its existing request helper):

```go
func TestCreateSiteAcceptsPixelKind(t *testing.T) {
	f := newPrivateFixture(t) // existing helper in this package; match its name
	res := f.post("/api/private/sites", `{"name":"Views","kind":"pixel","timezone":"UTC","retention_days":365}`)
	if res.Code != http.StatusCreated || !strings.Contains(res.Body.String(), `"kind":"pixel"`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body)
	}
}
```

- [ ] **Step 5: Run Go tests**

Run: `go build ./... && go vet ./... && go test ./internal/platform/... ./internal/sites/... ./internal/api/... -count=1`
Expected: all `ok`. (`internal/api` holds the OpenAPI contract test; it fails if the schema and handler disagree.)

- [ ] **Step 6: Commit (store + service)**

```bash
git add internal/sites internal/platform/store internal/platform/sitesvc
git commit -m "feat(sites): store and load the project kind"
```

- [ ] **Step 7: Commit (API + OpenAPI)**

```bash
git add internal/platform/http internal/api/openapi.yaml
git commit -m "feat(api): accept kind when creating a project"
```

- [ ] **Step 8: Regenerate the web client and commit**

Run: `cd web && bun run gen:api && bun run typecheck`
Expected: exit 0 (fix `SiteInput` users in `web/src/features/sites/types.ts` by adding `kind?: 'web' | 'pixel'` and making `hosts`/`revenue_currency` optional only if typecheck demands it).

```bash
git add web/src/lib/api.gen.ts web/packages/api-client/src/schema.ts web/src/features/sites/types.ts
git commit -m "chore(web): regenerate API client for project kind"
```

### Task 3: Pixel store (CRUD, limit, totals)

**Files:**
- Create: `internal/pixels/store.go`, `internal/pixels/store_test.go`

**Interfaces:**
- Produces:
  - `type Pixel struct { ID int64; SiteID int32; PublicID, Name, BadgeLabel string; Enabled bool; CreatedAt time.Time }`
  - `type DBTX interface { Exec(...); Query(...); QueryRow(...) }` (same shape as `links.DBTX`)
  - `var ErrNotFound, ErrLimit = errors.New("pixel not found"), errors.New("pixel limit reached")`
  - `const MaxPerSite = 100`
  - `func NewPublicID() (string, error)` — `"p"` + 11 chars from `[A-Za-z0-9]` via `crypto/rand`
  - `func Create(ctx, db DBTX, p Pixel) (Pixel, error)`, `Update(ctx, db, p Pixel) (Pixel, error)`, `Delete(ctx, db, siteID int32, id int64) error`, `List(ctx, db, siteID int32) ([]Pixel, error)`, `All(ctx, db) ([]Pixel, error)`
  - `func Totals(ctx, db DBTX) (map[int64]int64, error)` — all-time views per pixel

- [ ] **Step 1: Write the failing test**

```go
package pixels

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func newDB(t *testing.T) (*pgxpool.Pool, int32) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Pixels','pixels') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,kind) values ($1,'pixsite00001','P','Asia/Kathmandu',365,'pixel') returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	return pool, siteID
}

func TestNewPublicID(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		id, err := NewPublicID()
		if err != nil || len(id) != 12 || id[0] != 'p' || seen[id] {
			t.Fatalf("id = %q, %v", id, err)
		}
		seen[id] = true
	}
}

func TestStoreCRUDLimitAndTotals(t *testing.T) {
	pool, siteID := newDB(t)
	ctx := context.Background()
	created, err := Create(ctx, pool, Pixel{SiteID: siteID, Name: "GitHub profile", Enabled: true})
	if err != nil || created.ID == 0 || created.BadgeLabel != "views" || len(created.PublicID) != 12 {
		t.Fatalf("created = %+v, %v", created, err)
	}
	created.Name, created.BadgeLabel, created.Enabled = "Profile", "hits", false
	updated, err := Update(ctx, pool, created)
	if err != nil || updated.BadgeLabel != "hits" || updated.Enabled {
		t.Fatalf("updated = %+v, %v", updated, err)
	}
	if _, err := pool.Exec(ctx, `insert into pixel_views_daily values ($1,'2026-10-13','','proxy',5),($1,'2026-10-14','NP','direct',2)`, created.ID); err != nil {
		t.Fatal(err)
	}
	totals, err := Totals(ctx, pool)
	if err != nil || totals[created.ID] != 7 {
		t.Fatalf("totals = %v, %v", totals, err)
	}
	for i := 1; i < MaxPerSite; i++ {
		if _, err := Create(ctx, pool, Pixel{SiteID: siteID, Name: fmt.Sprintf("p%d", i), Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Create(ctx, pool, Pixel{SiteID: siteID, Name: "one too many", Enabled: true}); !errors.Is(err, ErrLimit) {
		t.Fatalf("101st err = %v", err)
	}
	if err := Delete(ctx, pool, siteID+1, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-site delete err = %v", err)
	}
	if err := Delete(ctx, pool, siteID, created.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := List(ctx, pool, siteID)
	if len(list) != MaxPerSite-1 {
		t.Fatalf("list len = %d", len(list))
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/pixels -count=1`
Expected: FAIL to compile, `undefined: Create`.

- [ ] **Step 3: Implement `internal/pixels/store.go`**

```go
// Package pixels serves view-counting images for pixel analytics projects.
package pixels

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const MaxPerSite = 100

var (
	ErrNotFound = errors.New("pixel not found")
	ErrLimit    = errors.New("pixel limit reached")
)

type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Pixel struct {
	ID         int64     `json:"id"`
	SiteID     int32     `json:"-"`
	PublicID   string    `json:"public_id"`
	Name       string    `json:"name"`
	BadgeLabel string    `json:"badge_label"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
}

const idAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func NewPublicID() (string, error) {
	raw := make([]byte, 11)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	out := make([]byte, 12)
	out[0] = 'p'
	for i, b := range raw {
		out[i+1] = idAlphabet[int(b)%len(idAlphabet)]
	}
	return string(out), nil
}

const columns = `id, site_id, public_id, name, badge_label, enabled, created_at`

func scan(row pgx.Row) (Pixel, error) {
	var p Pixel
	err := row.Scan(&p.ID, &p.SiteID, &p.PublicID, &p.Name, &p.BadgeLabel, &p.Enabled, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Pixel{}, ErrNotFound
	}
	return p, err
}

// Create inserts a pixel unless the site already has MaxPerSite. The count
// and insert are one statement, so concurrent creates cannot exceed the cap
// by more than the race window of a single snapshot; the site row lock below
// closes that window.
func Create(ctx context.Context, db DBTX, p Pixel) (Pixel, error) {
	if p.BadgeLabel == "" {
		p.BadgeLabel = "views"
	}
	if _, err := db.Exec(ctx, `select 1 from sites where id=$1 for update`, p.SiteID); err != nil {
		return Pixel{}, err
	}
	for range 3 {
		id, err := NewPublicID()
		if err != nil {
			return Pixel{}, err
		}
		out, err := scan(db.QueryRow(ctx, `
			insert into pixels(site_id, public_id, name, badge_label, enabled)
			select $1, $2, $3, $4, $5
			where (select count(*) from pixels where site_id=$1) < $6
			on conflict (public_id) do nothing
			returning `+columns, p.SiteID, id, p.Name, p.BadgeLabel, p.Enabled, MaxPerSite))
		if errors.Is(err, ErrNotFound) {
			var n int
			if err := db.QueryRow(ctx, `select count(*) from pixels where site_id=$1`, p.SiteID).Scan(&n); err != nil {
				return Pixel{}, err
			}
			if n >= MaxPerSite {
				return Pixel{}, ErrLimit
			}
			continue // public id collision; retry
		}
		return out, err
	}
	return Pixel{}, fmt.Errorf("allocate pixel public id: too many collisions")
}

func Update(ctx context.Context, db DBTX, p Pixel) (Pixel, error) {
	return scan(db.QueryRow(ctx, `
		update pixels set name=$3, badge_label=$4, enabled=$5
		where id=$1 and site_id=$2 returning `+columns,
		p.ID, p.SiteID, p.Name, p.BadgeLabel, p.Enabled))
}

func Delete(ctx context.Context, db DBTX, siteID int32, id int64) error {
	tag, err := db.Exec(ctx, `delete from pixels where id=$1 and site_id=$2`, id, siteID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func list(ctx context.Context, db DBTX, query string, args ...any) ([]Pixel, error) {
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Pixel, error) { return scan(row) })
}

func List(ctx context.Context, db DBTX, siteID int32) ([]Pixel, error) {
	return list(ctx, db, `select `+columns+` from pixels where site_id=$1 order by id`, siteID)
}

// All returns every pixel of a live (not deleted) site, for the registry.
func All(ctx context.Context, db DBTX) ([]Pixel, error) {
	return list(ctx, db, `
		select p.id, p.site_id, p.public_id, p.name, p.badge_label, p.enabled, p.created_at
		from pixels p join sites s on s.id = p.site_id
		where s.deleted_at is null and s.kind = 'pixel' order by p.id`)
}

func Totals(ctx context.Context, db DBTX) (map[int64]int64, error) {
	rows, err := db.Query(ctx, `select pixel_id, sum(views)::bigint from pixel_views_daily group by pixel_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var id, n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
```

Callers must run `Create` inside a transaction (the `for update` lock is per transaction); the API handler in Task 8 does.

- [ ] **Step 4: Run tests**

Run: `go vet ./internal/pixels && go test ./internal/pixels -count=1`
Expected: `ok supermetry/internal/pixels`.

- [ ] **Step 5: Commit**

```bash
git add internal/pixels/store.go internal/pixels/store_test.go
git commit -m "feat(pixels): add pixel store with per-project limit"
```

### Task 4: Pixel registry and TotalCache

**Files:**
- Create: `internal/pixels/registry.go`, `internal/pixels/totals.go`, `internal/pixels/registry_test.go`

**Interfaces:**
- Consumes: `All`, `Totals` (Task 3).
- Produces:
  - `type TotalCache struct` with `NewTotalCache() *TotalCache`, `Get(id int64) int64`, `Add(id int64, n int64)`, `Seed(map[int64]int64)` (seed keeps the max of seeded and in-memory value so a reload never moves a badge backwards)
  - `type Registry struct` with `NewRegistry(loader func(ctx) ([]Pixel, error), seed func(ctx) (map[int64]int64, error), totals *TotalCache) *Registry`, `Reload(ctx) error`, `ByPublicID(id string) (Pixel, bool)`, `Totals() *TotalCache`

- [ ] **Step 1: Write the failing test**

```go
package pixels

import (
	"context"
	"sync"
	"testing"
)

func TestTotalCacheAddGetSeed(t *testing.T) {
	c := NewTotalCache()
	c.Add(1, 2)
	c.Add(1, 3)
	if c.Get(1) != 5 || c.Get(2) != 0 {
		t.Fatalf("get = %d/%d", c.Get(1), c.Get(2))
	}
	c.Seed(map[int64]int64{1: 4, 2: 9})
	if c.Get(1) != 5 || c.Get(2) != 9 {
		t.Fatalf("after seed = %d/%d", c.Get(1), c.Get(2))
	}
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() { defer wg.Done(); c.Add(3, 1) }()
	}
	wg.Wait()
	if c.Get(3) != 50 {
		t.Fatalf("concurrent = %d", c.Get(3))
	}
}

func TestRegistryReloadAndLookup(t *testing.T) {
	pixels := []Pixel{{ID: 1, SiteID: 7, PublicID: "pAAAAAAAAAAA", Enabled: true}}
	r := NewRegistry(
		func(context.Context) ([]Pixel, error) { return pixels, nil },
		func(context.Context) (map[int64]int64, error) { return map[int64]int64{1: 42}, nil },
		NewTotalCache(),
	)
	if _, ok := r.ByPublicID("pAAAAAAAAAAA"); ok {
		t.Fatal("found before load")
	}
	if err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, ok := r.ByPublicID("pAAAAAAAAAAA")
	if !ok || p.SiteID != 7 || r.Totals().Get(1) != 42 {
		t.Fatalf("p=%+v ok=%v total=%d", p, ok, r.Totals().Get(1))
	}
	pixels = nil
	_ = r.Reload(context.Background())
	if _, ok := r.ByPublicID("pAAAAAAAAAAA"); ok {
		t.Fatal("deleted pixel still resolves")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/pixels -run 'TestTotalCache|TestRegistry' -count=1`
Expected: FAIL to compile, `undefined: NewTotalCache`.

- [ ] **Step 3: Implement**

`internal/pixels/totals.go`:

```go
package pixels

import "sync"

// TotalCache holds all-time view totals in memory so badges never read the
// database. It is seeded from Postgres and incremented on each counted view.
type TotalCache struct {
	mu     sync.RWMutex
	totals map[int64]int64
}

func NewTotalCache() *TotalCache { return &TotalCache{totals: map[int64]int64{}} }

func (c *TotalCache) Get(id int64) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.totals[id]
}

func (c *TotalCache) Add(id, n int64) {
	c.mu.Lock()
	c.totals[id] += n
	c.mu.Unlock()
}

// Seed merges persisted totals, keeping the larger value so unflushed
// in-memory increments are not lost and a badge never goes backwards.
func (c *TotalCache) Seed(persisted map[int64]int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, n := range persisted {
		if n > c.totals[id] {
			c.totals[id] = n
		}
	}
}
```

`internal/pixels/registry.go`:

```go
package pixels

import (
	"context"
	"sync/atomic"
)

// Registry resolves pixel public ids in memory. Reload swaps the whole map.
type Registry struct {
	load   func(context.Context) ([]Pixel, error)
	seed   func(context.Context) (map[int64]int64, error)
	totals *TotalCache
	byID   atomic.Pointer[map[string]Pixel]
	seeded atomic.Bool
}

func NewRegistry(load func(context.Context) ([]Pixel, error), seed func(context.Context) (map[int64]int64, error), totals *TotalCache) *Registry {
	r := &Registry{load: load, seed: seed, totals: totals}
	empty := map[string]Pixel{}
	r.byID.Store(&empty)
	return r
}

func (r *Registry) Totals() *TotalCache { return r.totals }

func (r *Registry) ByPublicID(id string) (Pixel, bool) {
	p, ok := (*r.byID.Load())[id]
	return p, ok
}

// Reload rereads pixels; totals are seeded from Postgres on the first load
// only (afterwards the in-memory cache is ahead of the database).
func (r *Registry) Reload(ctx context.Context) error {
	list, err := r.load(ctx)
	if err != nil {
		return err
	}
	if !r.seeded.Load() {
		totals, err := r.seed(ctx)
		if err != nil {
			return err
		}
		r.totals.Seed(totals)
		r.seeded.Store(true)
	}
	next := make(map[string]Pixel, len(list))
	for _, p := range list {
		next[p.PublicID] = p
	}
	r.byID.Store(&next)
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/pixels -count=1`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/pixels/registry.go internal/pixels/totals.go internal/pixels/registry_test.go
git commit -m "feat(pixels): add in-memory pixel registry and total cache"
```

### Task 5: Proxy detection, proxy limiter and view recorder

**Files:**
- Create: `internal/pixels/proxy.go`, `internal/pixels/proxy_test.go`, `internal/pixels/recorder.go`, `internal/pixels/recorder_test.go`

**Interfaces:**
- Consumes: `ingest.NewTokenBuckets(rate float64, burst int) ingest.RateLimiter` (`internal/ingest/ratelimit.go:54`), `TotalCache` (Task 4).
- Produces:
  - `func DetectProxy(ua string) (name string, ok bool)` — `"camo"` for UAs containing `github-camo`, `"google"` for `GoogleImageProxy`
  - `type ProxyLimiter struct`; `NewProxyLimiter() *ProxyLimiter` (20/s, burst 200); `Allow(key int64, proxy string) bool` — `key` is a pixel id (or counter id in Task 11)
  - `type ViewKey struct { PixelID int64; Day, Country, Via string }`
  - `type Recorder struct`; `NewRecorder(write WriteFunc, totals *TotalCache) *Recorder`; `type WriteFunc func(ctx context.Context, rows map[ViewKey]int64) error`; `PoolWriter(pool *pgxpool.Pool) WriteFunc`; `(*Recorder).View(k ViewKey)`; `Flush(ctx) error`; `Run(ctx) error` (flush every 10 s and on shutdown); `Dropped() int64`

- [ ] **Step 1: Write the failing tests**

`proxy_test.go`:

```go
package pixels

import "testing"

func TestDetectProxy(t *testing.T) {
	cases := map[string]string{
		"github-camo (876de43e)": "camo",
		"Mozilla/5.0 (Windows NT 5.1; rv:11.0) Gecko Firefox/11.0 (via ggpht.com GoogleImageProxy)": "google",
		"Mozilla/5.0 (X11; Linux x86_64) Chrome/124.0": "",
		"": "",
		"curl/8.5.0": "",
	}
	for ua, want := range cases {
		got, ok := DetectProxy(ua)
		if got != want || ok != (want != "") {
			t.Errorf("DetectProxy(%q) = %q,%v want %q", ua, got, ok, want)
		}
	}
}

func TestProxyLimiterIsPerPixelAndProxy(t *testing.T) {
	l := NewProxyLimiter()
	allowed := 0
	for range 500 {
		if l.Allow(1, "camo") {
			allowed++
		}
	}
	if allowed < 200 || allowed > 210 {
		t.Fatalf("burst allowed %d, want about 200", allowed)
	}
	if !l.Allow(2, "camo") || !l.Allow(1, "google") {
		t.Fatal("one busy pixel starved another key")
	}
}
```

`recorder_test.go`:

```go
package pixels

import (
	"context"
	"errors"
	"maps"
	"testing"
)

func TestRecorderAggregatesAndUpdatesTotals(t *testing.T) {
	var written map[ViewKey]int64
	totals := NewTotalCache()
	r := NewRecorder(func(_ context.Context, rows map[ViewKey]int64) error { written = maps.Clone(rows); return nil }, totals)
	k := ViewKey{PixelID: 1, Day: "2026-10-14", Via: "proxy"}
	r.View(k)
	r.View(k)
	r.View(ViewKey{PixelID: 1, Day: "2026-10-14", Country: "NP", Via: "direct"})
	if totals.Get(1) != 3 {
		t.Fatalf("total = %d", totals.Get(1))
	}
	if err := r.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if written[k] != 2 || len(written) != 2 {
		t.Fatalf("written = %v", written)
	}
}

func TestFlushFailureKeepsCountsAndTotals(t *testing.T) {
	fail := true
	var sum int64
	totals := NewTotalCache()
	r := NewRecorder(func(_ context.Context, rows map[ViewKey]int64) error {
		if fail {
			return errors.New("db down")
		}
		for _, n := range rows {
			sum += n
		}
		return nil
	}, totals)
	for range 3 {
		r.View(ViewKey{PixelID: 9, Day: "2026-10-14", Via: "proxy"})
	}
	if err := r.Flush(context.Background()); err == nil {
		t.Fatal("want error")
	}
	r.View(ViewKey{PixelID: 9, Day: "2026-10-14", Via: "proxy"})
	fail = false
	if err := r.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sum != 4 || totals.Get(9) != 4 {
		t.Fatalf("sum=%d total=%d, want 4/4", sum, totals.Get(9))
	}
}
```

Add a DB round-trip test to `store_test.go`:

```go
func TestPoolWriterUpsertsAndSkipsDeletedPixels(t *testing.T) {
	pool, siteID := newDB(t)
	ctx := context.Background()
	p, err := Create(ctx, pool, Pixel{SiteID: siteID, Name: "a", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	write := PoolWriter(pool)
	rows := map[ViewKey]int64{{PixelID: p.ID, Day: "2026-10-14", Via: "proxy"}: 2, {PixelID: 999999, Day: "2026-10-14", Via: "proxy"}: 1}
	for range 2 {
		if err := write(ctx, rows); err != nil {
			t.Fatal(err)
		}
	}
	var views int64
	_ = pool.QueryRow(ctx, `select coalesce(sum(views),0) from pixel_views_daily`).Scan(&views)
	if views != 4 {
		t.Fatalf("views = %d, want 4", views)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/pixels -count=1`
Expected: FAIL to compile, `undefined: DetectProxy`.

- [ ] **Step 3: Implement `proxy.go`**

```go
package pixels

import (
	"strconv"
	"strings"

	"supermetry/internal/ingest"
)

// Known image proxies, matched by user agent. They fetch on behalf of many
// viewers, so their requests skip the bot list and get their own limiter.
var proxies = []struct{ needle, name string }{
	{"github-camo", "camo"},
	{"GoogleImageProxy", "google"},
}

func DetectProxy(ua string) (string, bool) {
	for _, p := range proxies {
		if strings.Contains(ua, p.needle) {
			return p.name, true
		}
	}
	return "", false
}

// ProxyLimiter caps counted proxy fetches per pixel and proxy (20/s, burst
// 200), separately from the per-IP limiter.
type ProxyLimiter struct{ buckets ingest.RateLimiter }

func NewProxyLimiter() *ProxyLimiter { return &ProxyLimiter{buckets: ingest.NewTokenBuckets(20, 200)} }

func (l *ProxyLimiter) Allow(key int64, proxy string) bool {
	return l.buckets.Allow(strconv.FormatInt(key, 10) + ":" + proxy)
}
```

- [ ] **Step 4: Implement `recorder.go`**

```go
package pixels

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	flushEvery = 10 * time.Second
	maxKeys    = 100_000
)

type ViewKey struct {
	PixelID int64
	Day     string
	Country string
	Via     string
}

type WriteFunc func(ctx context.Context, rows map[ViewKey]int64) error

// Recorder buffers views in memory; request goroutines never touch the DB.
// Totals are bumped at record time, so a failed flush does not change badges.
type Recorder struct {
	write   WriteFunc
	totals  *TotalCache
	mu      sync.Mutex
	rows    map[ViewKey]int64
	flushMu sync.Mutex
	dropped atomic.Int64
}

func NewRecorder(write WriteFunc, totals *TotalCache) *Recorder {
	return &Recorder{write: write, totals: totals, rows: map[ViewKey]int64{}}
}

func (r *Recorder) Dropped() int64 { return r.dropped.Load() }

func (r *Recorder) View(k ViewKey) {
	r.mu.Lock()
	if _, ok := r.rows[k]; !ok && len(r.rows) >= maxKeys {
		r.mu.Unlock()
		r.dropped.Add(1)
		return
	}
	r.rows[k]++
	r.mu.Unlock()
	r.totals.Add(k.PixelID, 1)
}

func (r *Recorder) Flush(ctx context.Context) error {
	r.flushMu.Lock()
	defer r.flushMu.Unlock()
	r.mu.Lock()
	rows := r.rows
	r.rows = map[ViewKey]int64{}
	r.mu.Unlock()
	if len(rows) == 0 {
		return nil
	}
	if err := r.write(ctx, rows); err != nil {
		r.mu.Lock()
		for k, n := range rows {
			if _, ok := r.rows[k]; ok || len(r.rows) < maxKeys {
				r.rows[k] += n
			} else {
				r.dropped.Add(n)
			}
		}
		r.mu.Unlock()
		return err
	}
	return nil
}

func (r *Recorder) Run(ctx context.Context) error {
	ticker := time.NewTicker(flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return r.Flush(flushCtx)
		case <-ticker.C:
			if err := r.Flush(ctx); err != nil {
				slog.Warn("pixel view flush failed", "err", err)
			}
		}
	}
}

// PoolWriter upserts rows in one transaction; rows for deleted pixels are
// skipped by the existence guard.
func PoolWriter(pool *pgxpool.Pool) WriteFunc {
	return func(ctx context.Context, rows map[ViewKey]int64) error {
		batch := &pgx.Batch{}
		for k, n := range rows {
			batch.Queue(`
				insert into pixel_views_daily(pixel_id, day, country, via, views)
				select $1, $2::date, $3, $4, $5 where exists (select 1 from pixels where id=$1)
				on conflict (pixel_id, day, country, via)
				do update set views = pixel_views_daily.views + excluded.views`,
				k.PixelID, k.Day, k.Country, k.Via, n)
		}
		return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			return tx.SendBatch(ctx, batch).Close()
		})
	}
}
```

- [ ] **Step 5: Run tests**

Run: `go vet ./internal/pixels && go test -race ./internal/pixels -count=1`
Expected: `ok supermetry/internal/pixels`.

- [ ] **Step 6: Commit (two commits)**

```bash
git add internal/pixels/proxy.go internal/pixels/proxy_test.go
git commit -m "feat(pixels): detect image proxies and limit them per pixel"
git add internal/pixels/recorder.go internal/pixels/recorder_test.go internal/pixels/store_test.go
git commit -m "feat(pixels): buffer pixel views and flush daily aggregates"
```

### Task 6: SVG badge renderer and branding config

**Files:**
- Create: `internal/pixels/badge.go`, `internal/pixels/badge_test.go`
- Modify: `internal/config/config.go` (`BadgeBranding bool`), `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `func FormatCount(n int64) string`
  - `type BadgeOptions struct { Label string; Count int64; Style string; Color string; Branded bool }`
  - `func ParseBadgeQuery(q url.Values) (style, color string)` — invalid values fall back to `"flat"` / `""`
  - `func RenderBadge(o BadgeOptions) []byte`
  - `config.Config.BadgeBranding bool` from `SUPERMETRY_BADGE_BRANDING`, default `true`

- [ ] **Step 1: Write the failing tests**

```go
package pixels

import (
	"encoding/xml"
	"net/url"
	"strings"
	"testing"
)

func TestFormatCount(t *testing.T) {
	cases := map[int64]string{0: "0", 999: "999", 1000: "1k", 1234: "1.2k", 12_345: "12.3k", 999_949: "999.9k",
		999_950: "1M", 3_400_000: "3.4M", 1_234_567_890: "1.2B"}
	for n, want := range cases {
		if got := FormatCount(n); got != want {
			t.Errorf("FormatCount(%d) = %q, want %q", n, got, want)
		}
	}
}

func wellFormed(t *testing.T, svg []byte) {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(string(svg)))
	for {
		if _, err := d.Token(); err != nil {
			if err.Error() == "EOF" {
				return
			}
			t.Fatalf("invalid SVG: %v\n%s", err, svg)
		}
	}
}

func TestRenderEscapesLabel(t *testing.T) {
	svg := RenderBadge(BadgeOptions{Label: `<script>&"x"`, Count: 1234, Style: "flat"})
	wellFormed(t, svg)
	if strings.Contains(string(svg), "<script>") || !strings.Contains(string(svg), "1.2k") {
		t.Fatalf("svg = %s", svg)
	}
}

func TestRenderBrandingAndStyle(t *testing.T) {
	branded := string(RenderBadge(BadgeOptions{Label: "views", Count: 5, Style: "flat", Branded: true}))
	plain := string(RenderBadge(BadgeOptions{Label: "views", Count: 5, Style: "flat-square", Color: "ff0000"}))
	if !strings.Contains(branded, `id="sm-mark"`) || strings.Contains(plain, `id="sm-mark"`) {
		t.Fatal("branding mark toggle broken")
	}
	if !strings.Contains(plain, `#ff0000`) || strings.Contains(plain, `rx="3"`) || !strings.Contains(branded, `rx="3"`) {
		t.Fatal("style/color not applied")
	}
	if !strings.Contains(branded, `height="20"`) {
		t.Fatal("badge must be 20px high")
	}
}

func TestParseBadgeQuery(t *testing.T) {
	s, c := ParseBadgeQuery(url.Values{"style": {"flat-square"}, "color": {"0a0B0c"}})
	if s != "flat-square" || c != "0a0b0c" {
		t.Fatalf("%q %q", s, c)
	}
	s, c = ParseBadgeQuery(url.Values{"style": {"plastic"}, "color": {"red;}<"}})
	if s != "flat" || c != "" {
		t.Fatalf("fallback %q %q", s, c)
	}
}
```

Config test (append to `internal/config/config_test.go`, using that file's `values` map helper as in `TestLoadHomepageURL`):

```go
func TestLoadBadgeBranding(t *testing.T) {
	values := baseValues() // the helper TestLoadHomepageURL uses; match its name
	c, err := Load(mapGetenv(values))
	if err != nil || !c.BadgeBranding {
		t.Fatalf("default BadgeBranding = %v, %v", c.BadgeBranding, err)
	}
	values["SUPERMETRY_BADGE_BRANDING"] = "false"
	c, err = Load(mapGetenv(values))
	if err != nil || c.BadgeBranding {
		t.Fatalf("BadgeBranding = %v, %v", c.BadgeBranding, err)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/pixels ./internal/config -count=1`
Expected: FAIL to compile, `undefined: FormatCount` / `c.BadgeBranding undefined`.

- [ ] **Step 3: Implement `badge.go`**

Colours come from `web/packages/tokens/tokens.json` (light theme): label = `text.default` `oklch(0.18 0.03 285)` ≈ `#1c1a2b`, value = `accent.default` `oklch(0.51 0.25 281)` ≈ `#5b3cf0`. The mark is our own: three rising bars.

```go
package pixels

import (
	"bytes"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	labelColor   = "#1c1a2b"
	defaultColor = "#5b3cf0"
	charWidth    = 7 // px per character at 11px Verdana-ish; badges need not be exact
	padding      = 6
	markWidth    = 14
)

var hexColor = regexp.MustCompile(`^[0-9a-fA-F]{6}$`)

// FormatCount rounds to one decimal with integer maths (no float edge cases):
// 1234 -> 1.2k, 999_950 -> 1M, 1_234_567_890 -> 1.2B.
func FormatCount(n int64) string {
	units := []struct {
		div    int64
		suffix string
	}{{1_000_000_000, "B"}, {1_000_000, "M"}, {1_000, "k"}}
	for i, u := range units {
		if n < u.div {
			continue
		}
		tenths := (n*10 + u.div/2) / u.div
		if tenths >= 10_000 && i > 0 {
			return "1" + units[i-1].suffix
		}
		if tenths%10 == 0 {
			return strconv.FormatInt(tenths/10, 10) + u.suffix
		}
		return strconv.FormatInt(tenths/10, 10) + "." + strconv.FormatInt(tenths%10, 10) + u.suffix
	}
	return strconv.FormatInt(n, 10)
}

func ParseBadgeQuery(q url.Values) (style, color string) {
	style = q.Get("style")
	if style != "flat-square" {
		style = "flat"
	}
	if c := q.Get("color"); hexColor.MatchString(c) {
		color = strings.ToLower(c)
	}
	return style, color
}

type BadgeOptions struct {
	Label   string
	Count   int64
	Style   string
	Color   string
	Branded bool
}

func RenderBadge(o BadgeOptions) []byte {
	value := FormatCount(o.Count)
	label := html.EscapeString(o.Label)
	left := len([]rune(o.Label))*charWidth + 2*padding
	textX := padding
	if o.Branded {
		left += markWidth + 2
		textX += markWidth + 2
	}
	right := len(value)*charWidth + 2*padding
	width := left + right
	fill := defaultColor
	if o.Color != "" {
		fill = "#" + o.Color
	}
	rx := ""
	if o.Style != "flat-square" {
		rx = ` rx="3"`
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s: %s">`, width, label, value)
	fmt.Fprintf(&b, `<title>%s: %s</title><clipPath id="r"><rect width="%d" height="20"%s/></clipPath><g clip-path="url(#r)">`, label, value, width, rx)
	fmt.Fprintf(&b, `<rect width="%d" height="20" fill="%s"/><rect x="%d" width="%d" height="20" fill="%s"/></g>`, left, labelColor, left, right, fill)
	if o.Branded {
		b.WriteString(`<g id="sm-mark" fill="#fff"><rect x="6" y="11" width="3" height="4"/><rect x="10" y="8" width="3" height="7"/><rect x="14" y="5" width="3" height="10"/></g>`)
	}
	fmt.Fprintf(&b, `<g fill="#fff" font-family="Verdana,DejaVu Sans,sans-serif" font-size="11"><text x="%d" y="14">%s</text><text x="%d" y="14">%s</text></g></svg>`,
		textX, label, left+padding, value)
	return b.Bytes()
}
```

`n*10` overflows only above 9.2e17 views, far beyond any real counter.

- [ ] **Step 4: Config**

In `internal/config/config.go`: add `BadgeBranding bool` to `Config`, set `BadgeBranding: true` in the literal next to `HomepageURL`, and after the replay parsing add:

```go
	parseOptionalBool(getenv("SUPERMETRY_BADGE_BRANDING"), "SUPERMETRY_BADGE_BRANDING", &c.BadgeBranding, &errs)
```

- [ ] **Step 5: Run tests**

Run: `go vet ./internal/pixels ./internal/config && go test ./internal/pixels ./internal/config -count=1`
Expected: both `ok`.

- [ ] **Step 6: Commit (two commits)**

```bash
git add internal/pixels/badge.go internal/pixels/badge_test.go
git commit -m "feat(pixels): render view-count SVG badges"
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add SUPERMETRY_BADGE_BRANDING"
```

### Task 7: Pixel image handler (`.gif` and `.svg`) and wiring

**Files:**
- Create: `internal/pixels/handler.go`, `internal/pixels/handler_test.go`
- Modify: `internal/links/handler.go:209` (export `TransparentGIF` var; keep `ServePixel` unchanged otherwise)
- Modify: `internal/api/router.go:75,177-180` (`Pixels http.HandlerFunc` dep; route `GET /px/{file}` to it when set)
- Modify: `cmd/supermetry/serve.go:~325` (build registry, recorder, handler; reload registry at start; run recorder)

**Interfaces:**
- Consumes: `Registry`, `Recorder`, `ProxyLimiter`, `DetectProxy`, `RenderBadge`, `ParseBadgeQuery` (Tasks 4–6); `sites.Registry.Site(id)`; `links.Enricher` interface (`BotWithBlockedReferrers`, `Country`).
- Produces: `type Handler struct { Pixels *Registry; Recorder *Recorder; Sites sites.Registry; Enricher links.Enricher; IPLimiter ingest.RateLimiter; ProxyLimiter *ProxyLimiter; Fallback http.HandlerFunc; ClientIP func(*http.Request) (key string, ip netip.Addr); Branded bool; Now func() time.Time }`; `(*Handler).ServeHTTP`.

- [ ] **Step 1: Write the failing test**

```go
package pixels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"supermetry/internal/enrich"
	"supermetry/internal/sites"
)

type siteReg map[int32]sites.Site

func (r siteReg) ByPublicID(string) (sites.Site, bool) { return sites.Site{}, false }
func (r siteReg) Site(id int32) (sites.Site, bool)    { s, ok := r[id]; return s, ok }
func (r siteReg) All() []sites.Site                    { return nil }

const (
	camoUA   = "github-camo (876de43e)"
	chromeUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	botUA    = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
)

type pxFixture struct {
	h        *Handler
	rows     map[ViewKey]int64
	fallback int
}

func newPxFixture(t *testing.T) *pxFixture {
	t.Helper()
	enricher, err := enrich.New("")
	if err != nil {
		t.Fatal(err)
	}
	f := &pxFixture{rows: map[ViewKey]int64{}}
	reg := NewRegistry(func(context.Context) ([]Pixel, error) {
		return []Pixel{
			{ID: 1, SiteID: 1, PublicID: "pAAAAAAAAAAA", BadgeLabel: "views", Enabled: true},
			{ID: 2, SiteID: 1, PublicID: "pDISABLED000", BadgeLabel: "views", Enabled: false},
		}, nil
	}, func(context.Context) (map[int64]int64, error) { return map[int64]int64{1: 1233}, nil }, NewTotalCache())
	if err := reg.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.h = &Handler{
		Pixels:       reg,
		Recorder:     NewRecorder(func(_ context.Context, r map[ViewKey]int64) error { for k, n := range r { f.rows[k] += n }; return nil }, reg.Totals()),
		Sites:        siteReg{1: {ID: 1, Kind: sites.KindPixel, RespectPrivacySignals: true}},
		Enricher:     enricher,
		ProxyLimiter: NewProxyLimiter(),
		Fallback:     func(w http.ResponseWriter, r *http.Request) { f.fallback++; w.WriteHeader(http.StatusOK) },
		ClientIP:     func(*http.Request) (string, netip.Addr) { return "k", netip.MustParseAddr("203.0.113.9") },
		Branded:      true,
		Now:          func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) },
	}
	return f
}

func (f *pxFixture) get(t *testing.T, method, path, ua string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("User-Agent", ua)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	mux := http.NewServeMux()
	mux.Handle("GET /px/{file}", f.h)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	_ = f.h.Recorder.Flush(context.Background())
	return rec
}

func (f *pxFixture) total() (n int64) {
	for _, v := range f.rows {
		n += v
	}
	return n
}

func TestCamoIsCountedAsProxyAndBadgeShowsTotal(t *testing.T) {
	f := newPxFixture(t)
	res := f.get(t, http.MethodGet, "/px/pAAAAAAAAAAA.svg", camoUA)
	if res.Code != 200 || res.Header().Get("Content-Type") != "image/svg+xml" || res.Header().Get("Cache-Control") != "no-store, max-age=0" {
		t.Fatalf("status=%d headers=%v", res.Code, res.Header())
	}
	if !strings.Contains(res.Body.String(), "1.2k") {
		t.Fatalf("badge = %s", res.Body)
	}
	if f.rows[ViewKey{PixelID: 1, Day: "2026-10-14", Via: "proxy"}] != 1 {
		t.Fatalf("rows = %v", f.rows)
	}
}

func TestDirectViewGetsCountryAndGIF(t *testing.T) {
	f := newPxFixture(t)
	res := f.get(t, http.MethodGet, "/px/pAAAAAAAAAAA.gif", chromeUA)
	if res.Code != 200 || res.Header().Get("Content-Type") != "image/gif" || res.Body.Len() != 43 {
		t.Fatalf("gif: %d %v %d", res.Code, res.Header(), res.Body.Len())
	}
	for k := range f.rows {
		if k.Via != "direct" {
			t.Fatalf("row = %+v", k)
		}
	}
	if f.total() != 1 {
		t.Fatalf("total = %d", f.total())
	}
}

func TestNotCountedButImageServed(t *testing.T) {
	f := newPxFixture(t)
	for _, tc := range []struct{ path, ua string; h []string }{
		{"/px/pAAAAAAAAAAA.svg", botUA, nil},
		{"/px/pDISABLED000.svg", camoUA, nil},
		{"/px/pUNKNOWN0000.svg", camoUA, nil},
		{"/px/pAAAAAAAAAAA.gif", chromeUA, []string{"Sec-GPC", "1"}},
	} {
		res := f.get(t, http.MethodGet, tc.path, tc.ua, tc.h...)
		if res.Code != 200 || res.Body.Len() == 0 {
			t.Errorf("%s: status %d", tc.path, res.Code)
		}
	}
	if f.total() != 0 {
		t.Fatalf("counted %v", f.rows)
	}
}

func TestOddFileNamesServeImageWithoutCounting(t *testing.T) {
	f := newPxFixture(t)
	for _, path := range []string{"/px/pAAAAAAAAAAA.svg.gif", "/px/.svg", "/px/p" + strings.Repeat("A", 200) + ".svg"} {
		if res := f.get(t, http.MethodGet, path, camoUA); res.Code != 200 {
			t.Errorf("%s: %d", path, res.Code)
		}
	}
	if res := f.get(t, http.MethodHead, "/px/pAAAAAAAAAAA.svg", camoUA); res.Code != 200 || res.Body.Len() != 0 {
		t.Errorf("HEAD: %d len %d", res.Code, res.Body.Len())
	}
	if f.total() != 0 {
		t.Fatalf("counted %v", f.rows)
	}
}

func TestWebPixelStillServedForSiteIDStartingWithP(t *testing.T) {
	f := newPxFixture(t)
	f.get(t, http.MethodGet, "/px/pubsite00001.gif?c=spring", chromeUA)
	if f.fallback != 1 || f.total() != 0 {
		t.Fatalf("fallback=%d rows=%v", f.fallback, f.rows)
	}
}
```

Note the mux in the test registers `GET`, which also serves `HEAD`; the handler must treat HEAD as "serve, don't count, no body".

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/pixels -run 'Camo|Direct|NotCounted|Odd|WebPixel' -count=1`
Expected: FAIL to compile, `undefined: Handler`.

- [ ] **Step 3: Export the GIF bytes in links**

In `internal/links/handler.go` rename `transparentGIF` to `TransparentGIF` (all uses; `grep -n transparentGIF internal/links`).

- [ ] **Step 4: Implement `internal/pixels/handler.go`**

```go
package pixels

import (
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"supermetry/internal/enrich"
	"supermetry/internal/ingest"
	"supermetry/internal/links"
	"supermetry/internal/sites"
)

type Handler struct {
	Pixels       *Registry
	Recorder     *Recorder
	Sites        sites.Registry
	Enricher     links.Enricher
	IPLimiter    ingest.RateLimiter
	ProxyLimiter *ProxyLimiter
	Fallback     http.HandlerFunc // web-project pixels (/px/<site id>.gif)
	ClientIP     func(*http.Request) (string, netip.Addr)
	Branded      bool
	Now          func() time.Time
}

// ServeHTTP answers GET/HEAD /px/{file}. Pixel ids win; anything else that
// ends in .gif goes to the web-project pixel. It never returns an error status.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	id, ext, _ := strings.Cut(file, ".")
	pixel, known := h.Pixels.ByPublicID(id)
	if !known && ext == "gif" && h.Fallback != nil {
		h.Fallback(w, r)
		return
	}
	if known && (ext == "gif" || ext == "svg") {
		h.count(r, pixel)
	}
	if ext == "svg" {
		label, total := "views", int64(0)
		if known {
			label, total = pixel.BadgeLabel, h.Pixels.Totals().Get(pixel.ID)
		}
		style, color := ParseBadgeQuery(r.URL.Query())
		write(w, r, "image/svg+xml", RenderBadge(BadgeOptions{Label: label, Count: total, Style: style, Color: color, Branded: h.Branded}))
		return
	}
	write(w, r, "image/gif", links.TransparentGIF)
}

func write(w http.ResponseWriter, r *http.Request, contentType string, body []byte) {
	header := w.Header()
	header.Set("Content-Type", contentType)
	header.Set("Cache-Control", "no-store, max-age=0")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func (h *Handler) count(r *http.Request, pixel Pixel) {
	if r.Method != http.MethodGet || !pixel.Enabled {
		return
	}
	site, ok := h.Sites.Site(pixel.SiteID)
	if !ok || site.Kind != sites.KindPixel {
		return
	}
	day := h.Now().In(location(site)).Format(time.DateOnly)
	if proxy, ok := DetectProxy(r.UserAgent()); ok {
		if h.ProxyLimiter.Allow(pixel.ID, proxy) {
			h.Recorder.View(ViewKey{PixelID: pixel.ID, Day: day, Via: "proxy"})
		}
		return
	}
	key, ip := h.ClientIP(r)
	if h.IPLimiter != nil && !h.IPLimiter.Allow("pxv:"+key) {
		return
	}
	if h.Enricher.BotWithBlockedReferrers(r.UserAgent(), ip, "", site.BlockedReferrers) != enrich.BotNone {
		return
	}
	if site.RespectPrivacySignals && (r.Header.Get("DNT") == "1" || r.Header.Get("Sec-GPC") == "1") {
		return
	}
	h.Recorder.View(ViewKey{PixelID: pixel.ID, Day: day, Country: h.Enricher.Country(ip), Via: "direct"})
}

func location(site sites.Site) *time.Location {
	if site.TZ == nil {
		return time.UTC
	}
	return site.TZ
}
```

If `links.Enricher` lacks `Country(netip.Addr) string`, check `internal/links/handler.go:34` and use the method it declares.

- [ ] **Step 5: Run handler tests**

Run: `go vet ./internal/pixels ./internal/links && go test ./internal/pixels ./internal/links -count=1`
Expected: both `ok`.

- [ ] **Step 6: Commit the handler**

```bash
git add internal/pixels/handler.go internal/pixels/handler_test.go internal/links/handler.go
git commit -m "feat(pixels): serve pixel GIFs and SVG badges"
```

- [ ] **Step 7: Wire into the server**

`internal/api/router.go`: add to deps `Pixels http.Handler // pixel projects; wraps Links.ServePixel. Optional.` and in the routes block:

```go
	if deps.Links != nil {
		mux.HandleFunc("GET /l/{slug}", deps.Links.ServeRedirect)
		if deps.Pixels != nil {
			mux.Handle("GET /px/{file}", deps.Pixels)
		} else {
			mux.HandleFunc("GET /px/{file}", deps.Links.ServePixel)
		}
	}
```

`internal/links/handler.go`: export `func (h *Handler) ClientKey(r *http.Request) (string, netip.Addr) { return h.clientKey(r) }`.

`cmd/supermetry/serve.go` after `linkHandler` (around line 333):

```go
	pixelTotals := pixels.NewTotalCache()
	pixelRegistry := pixels.NewRegistry(
		func(ctx context.Context) ([]pixels.Pixel, error) { return pixels.All(ctx, pool) },
		func(ctx context.Context) (map[int64]int64, error) { return pixels.Totals(ctx, pool) },
		pixelTotals)
	if err := pixelRegistry.Reload(ctx); err != nil {
		return fmt.Errorf("load pixels: %w", err)
	}
	pixelRecorder := pixels.NewRecorder(pixels.PoolWriter(pool), pixelTotals)
	pixelHandler := &pixels.Handler{
		Pixels: pixelRegistry, Recorder: pixelRecorder, Sites: registry, Enricher: enricher,
		IPLimiter: ingest.NewTokenBuckets(10, 60), ProxyLimiter: pixels.NewProxyLimiter(),
		Fallback: linkHandler.ServePixel, ClientIP: linkHandler.ClientKey,
		Branded: cfg.BadgeBranding, Now: settings.clock.Now,
	}
```

Pass `Pixels: pixelHandler` in the router deps (line ~433) and start `pixelRecorder.Run` exactly where `linkRecorder.Run` is started (`grep -n "linkRecorder.Run" cmd/supermetry/serve.go`), so it shares the shutdown flush. Keep `pixelRegistry` in scope for Task 8.

- [ ] **Step 8: Verify and commit wiring**

Run: `go build ./... && go vet ./... && go test ./internal/api ./cmd/supermetry -count=1`
Expected: `ok` for both.

```bash
git add internal/api/router.go internal/links/handler.go cmd/supermetry/serve.go
git commit -m "feat(server): route pixel project images"
```

### Task 8: Pixel stats query, private API and OpenAPI

**Files:**
- Create: `internal/pixels/stats.go`, `internal/pixels/stats_test.go`
- Create: `internal/platform/http/handlers_pixels.go`, `internal/platform/http/handlers_pixels_test.go`
- Modify: `internal/platform/http/private.go:~272` (routes), `Deps` struct (`Pixels *pixels.Registry`)
- Modify: `internal/platform/http/handlers_links_test.go:27-116` (keep `registry` on `linkFixture` so pixel tests can call `Invalidate()`)
- Modify: `cmd/supermetry/serve.go` (`Pixels: pixelRegistry` in `platformhttp.Deps`)
- Modify: `internal/api/openapi.yaml` (paths + schemas)

**Interfaces:**
- Consumes: Task 3 store, Task 4 `Registry.Reload`/`Totals()`; existing `sitePrincipal`, `statsRange`, `linkRoles()`, `authz.ActionLinksManage`, `audit.Write`, `h.linkBase(r)`.
- Produces:
  - `type DayViews struct { Day string \`json:"day"\`; Proxy int64 \`json:"proxy"\`; Direct int64 \`json:"direct"\` }`
  - `type PixelViews struct { PixelID int64 \`json:"pixel_id"\`; Views int64 \`json:"views"\` }`
  - `type CountryViews struct { Country string \`json:"country"\`; Views int64 \`json:"views"\` }`
  - `type Stats struct { Daily []DayViews; Pixels []PixelViews; Countries []CountryViews }` (json `daily`, `pixels`, `countries`)
  - `func StatsFor(ctx, db DBTX, siteID int32, from, to string, pixelID int64) (Stats, error)` (`pixelID == 0` means all)
  - `func Views30(ctx, db DBTX, siteID int32, today string) (map[int64]int64, error)`
  - HTTP: `GET/POST /api/private/sites/{site}/pixels`, `PATCH/DELETE /api/private/sites/{site}/pixels/{id}`, `GET /api/private/sites/{site}/pixels/stats?from=&to=&pixel=`
  - JSON `pixelJSON { id, public_id, name, badge_label, enabled, created_at, total, views_30d, gif_url, svg_url }`

Decision: the stats endpoint takes `from`/`to` (same as link stats, via `statsRange`) instead of the spec's `period=`; the web client maps its period picker to dates. Non-pixel projects get `404 problem+json` ("not a pixel project") on all pixel routes.

- [ ] **Step 1: Write the failing store test** (`internal/pixels/stats_test.go`)

```go
package pixels

import (
	"context"
	"testing"
)

func TestStatsAgainstHandComputedFixture(t *testing.T) {
	pool, siteID := newDB(t)
	ctx := context.Background()
	a, _ := Create(ctx, pool, Pixel{SiteID: siteID, Name: "a", Enabled: true})
	b, _ := Create(ctx, pool, Pixel{SiteID: siteID, Name: "b", Enabled: true})
	_, err := pool.Exec(ctx, `insert into pixel_views_daily values
		($1,'2026-10-01','','proxy',10), ($1,'2026-10-01','NP','direct',2), ($1,'2026-10-02','IN','direct',3),
		($2,'2026-10-02','','proxy',5),  ($2,'2026-09-30','NP','direct',100)`, a.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	s, err := StatsFor(ctx, pool, siteID, "2026-10-01", "2026-10-02", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []DayViews{{"2026-10-01", 10, 2}, {"2026-10-02", 5, 3}}
	if len(s.Daily) != 2 || s.Daily[0] != want[0] || s.Daily[1] != want[1] {
		t.Fatalf("daily = %+v", s.Daily)
	}
	if len(s.Pixels) != 2 || s.Pixels[0] != (PixelViews{a.ID, 15}) || s.Pixels[1] != (PixelViews{b.ID, 5}) {
		t.Fatalf("pixels = %+v", s.Pixels)
	}
	if len(s.Countries) != 2 || s.Countries[0] != (CountryViews{"IN", 3}) || s.Countries[1] != (CountryViews{"NP", 2}) {
		t.Fatalf("countries = %+v", s.Countries)
	}
	only, _ := StatsFor(ctx, pool, siteID, "2026-10-01", "2026-10-02", b.ID)
	if len(only.Daily) != 1 || only.Daily[0] != (DayViews{"2026-10-02", 5, 0}) {
		t.Fatalf("filtered = %+v", only.Daily)
	}
	v30, _ := Views30(ctx, pool, siteID, "2026-10-02")
	if v30[a.ID] != 15 || v30[b.ID] != 105 {
		t.Fatalf("views30 = %v", v30)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/pixels -run TestStats -count=1`
Expected: FAIL to compile, `undefined: StatsFor`.

- [ ] **Step 3: Implement `internal/pixels/stats.go`**

```go
package pixels

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type DayViews struct {
	Day    string `json:"day"`
	Proxy  int64  `json:"proxy"`
	Direct int64  `json:"direct"`
}
type PixelViews struct {
	PixelID int64 `json:"pixel_id"`
	Views   int64 `json:"views"`
}
type CountryViews struct {
	Country string `json:"country"`
	Views   int64  `json:"views"`
}
type Stats struct {
	Daily     []DayViews     `json:"daily"`
	Pixels    []PixelViews   `json:"pixels"`
	Countries []CountryViews `json:"countries"`
}

// Rows are scoped through pixels.site_id, so a pixel id from another site
// returns nothing.
const scope = `from pixel_views_daily v join pixels p on p.id = v.pixel_id
	where p.site_id = $1 and v.day between $2::date and $3::date and ($4 = 0 or v.pixel_id = $4)`

func StatsFor(ctx context.Context, db DBTX, siteID int32, from, to string, pixelID int64) (Stats, error) {
	var s Stats
	var err error
	args := []any{siteID, from, to, pixelID}
	if s.Daily, err = collect(ctx, db, `select v.day::text,
		coalesce(sum(v.views) filter (where v.via='proxy'),0)::bigint,
		coalesce(sum(v.views) filter (where v.via='direct'),0)::bigint `+scope+` group by v.day order by v.day`,
		args, func(r pgx.CollectableRow) (DayViews, error) { var d DayViews; return d, r.Scan(&d.Day, &d.Proxy, &d.Direct) }); err != nil {
		return Stats{}, err
	}
	if s.Pixels, err = collect(ctx, db, `select v.pixel_id, sum(v.views)::bigint `+scope+` group by v.pixel_id order by v.pixel_id`,
		args, func(r pgx.CollectableRow) (PixelViews, error) { var p PixelViews; return p, r.Scan(&p.PixelID, &p.Views) }); err != nil {
		return Stats{}, err
	}
	if s.Countries, err = collect(ctx, db, `select v.country, sum(v.views)::bigint `+scope+` and v.via='direct' and v.country <> ''
		group by v.country order by 2 desc, 1 limit 250`,
		args, func(r pgx.CollectableRow) (CountryViews, error) { var c CountryViews; return c, r.Scan(&c.Country, &c.Views) }); err != nil {
		return Stats{}, err
	}
	return s, nil
}

func collect[T any](ctx context.Context, db DBTX, q string, args []any, fn pgx.RowToFunc[T]) ([]T, error) {
	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, fn)
	if out == nil {
		out = []T{}
	}
	return out, err
}

func Views30(ctx context.Context, db DBTX, siteID int32, today string) (map[int64]int64, error) {
	rows, err := db.Query(ctx, `select v.pixel_id, sum(v.views)::bigint from pixel_views_daily v join pixels p on p.id=v.pixel_id
		where p.site_id=$1 and v.day > $2::date - 30 and v.day <= $2::date group by v.pixel_id`, siteID, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var id, n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
```

The primary key `(pixel_id, day, …)` serves these range scans; no extra index (CLAUDE.md: only indexes queries prove they need).

- [ ] **Step 4: Run and commit the query**

Run: `go test ./internal/pixels -count=1`
Expected: `ok`.

```bash
git add internal/pixels/stats.go internal/pixels/stats_test.go
git commit -m "feat(pixels): query daily, per-pixel and country views"
```

- [ ] **Step 5: Write the failing API test** (`internal/platform/http/handlers_pixels_test.go`). First add `registry *sitesvc.Registry` to `linkFixture` and set it in `newLinkFixture`; also pass `Pixels: pixelReg` built from `pixels.NewRegistry(... pixels.All/Totals on pool ...)` and store it as `pixels *pixels.Registry`.

```go
func newPixelFixture(t *testing.T) linkFixture {
	t.Helper()
	f := newLinkFixture(t)
	if _, err := f.pool.Exec(context.Background(), `update sites set kind='pixel' where id=$1`, f.siteID); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestPixelsCRUDLimitAndStats(t *testing.T) {
	f := newPixelFixture(t)
	base := fmt.Sprintf("/api/private/sites/%d/pixels", f.siteID)
	res := f.request(t, "editor", http.MethodPost, base, `{"name":"GitHub profile"}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("create %d %s", res.Code, res.Body)
	}
	var created map[string]any
	_ = json.NewDecoder(res.Body).Decode(&created)
	publicID := created["public_id"].(string)
	if created["badge_label"] != "views" || created["svg_url"] != "https://sm.example.test/px/"+publicID+".svg" {
		t.Fatalf("created = %v", created)
	}
	if _, ok := f.pixels.ByPublicID(publicID); !ok {
		t.Fatal("registry not reloaded after create")
	}
	id := int64(created["id"].(float64))
	if res := f.request(t, "viewer", http.MethodPost, base, `{"name":"x"}`); res.Code != http.StatusForbidden {
		t.Fatalf("viewer create = %d", res.Code)
	}
	if res := f.request(t, "editor", http.MethodPost, base, `{"name":""}`); res.Code != http.StatusBadRequest {
		t.Fatalf("empty name = %d", res.Code)
	}
	if res := f.request(t, "editor", http.MethodPatch, fmt.Sprintf("%s/%d", base, id), `{"badge_label":"hits","enabled":false}`); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"enabled":false`) {
		t.Fatalf("patch %d %s", res.Code, res.Body)
	}
	for i := 1; i < 100; i++ {
		f.request(t, "editor", http.MethodPost, base, fmt.Sprintf(`{"name":"p%d"}`, i))
	}
	res = f.request(t, "editor", http.MethodPost, base, `{"name":"too many"}`)
	if res.Code != http.StatusConflict || res.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("101st = %d %s", res.Code, res.Header())
	}
	if _, err := f.pool.Exec(context.Background(), `insert into pixel_views_daily values ($1,'2026-10-12','','proxy',4)`, id); err != nil {
		t.Fatal(err)
	}
	res = f.request(t, "viewer", http.MethodGet, base+"/stats?from=2026-10-01&to=2026-10-13", "")
	if res.Code != 200 || !strings.Contains(res.Body.String(), `{"day":"2026-10-12","proxy":4,"direct":0}`) {
		t.Fatalf("stats %d %s", res.Code, res.Body)
	}
	if res := f.request(t, "editor", http.MethodDelete, fmt.Sprintf("%s/%d", base, id), ""); res.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", res.Code)
	}
	web := fmt.Sprintf("/api/private/sites/%d/pixels", f.otherSite)
	if res := f.request(t, "owner", http.MethodGet, web, ""); res.Code != http.StatusNotFound {
		t.Fatalf("web project pixels = %d", res.Code)
	}
}
```

- [ ] **Step 6: Run to verify it fails**

Run: `go test ./internal/platform/http -run TestPixels -count=1`
Expected: FAIL (`f.registry undefined` / 404 for the routes).

- [ ] **Step 7: Implement `handlers_pixels.go`**

```go
package platformhttp

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	"supermetry/internal/pixels"
	coresites "supermetry/internal/sites"
)

type pixelInput struct {
	Name       *string `json:"name"`
	BadgeLabel *string `json:"badge_label"`
	Enabled    *bool   `json:"enabled"`
}

type pixelJSON struct {
	pixels.Pixel
	Total    int64  `json:"total"`
	Views30d int64  `json:"views_30d"`
	GIFURL   string `json:"gif_url"`
	SVGURL   string `json:"svg_url"`
}

func (h *handler) pixelSite(w http.ResponseWriter, r *http.Request, action authz.Action) (authz.Principal, coresites.Site, bool) {
	principal, site, ok := h.sitePrincipal(w, r, action, linkRoles()...)
	if !ok {
		return principal, site, false
	}
	if site.Kind != coresites.KindPixel || h.deps.Store == nil || h.deps.Pixels == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "not a pixel project")
		return principal, site, false
	}
	return principal, site, true
}

func (h *handler) toPixelJSON(r *http.Request, p pixels.Pixel, v30 map[int64]int64) pixelJSON {
	base := h.linkBase(r) + "/px/" + p.PublicID
	return pixelJSON{Pixel: p, Total: h.deps.Pixels.Totals().Get(p.ID), Views30d: v30[p.ID], GIFURL: base + ".gif", SVGURL: base + ".svg"}
}

func applyPixelInput(p *pixels.Pixel, in pixelInput) error {
	if in.Name != nil {
		p.Name = strings.TrimSpace(*in.Name)
	}
	if in.BadgeLabel != nil {
		p.BadgeLabel = strings.TrimSpace(*in.BadgeLabel)
	}
	if in.Enabled != nil {
		p.Enabled = *in.Enabled
	}
	if n := len([]rune(p.Name)); n < 1 || n > 120 {
		return errors.New("name must be 1 to 120 characters")
	}
	if n := len([]rune(p.BadgeLabel)); p.BadgeLabel != "" && n > 40 {
		return errors.New("badge_label must be at most 40 characters")
	}
	return nil
}

func (h *handler) listPixels(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.pixelSite(w, r, authz.ActionStatsRead)
	if !ok {
		return
	}
	rows, err := pixels.List(r.Context(), h.deps.Store.Pool, site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	v30, err := pixels.Views30(r.Context(), h.deps.Store.Pool, site.ID, h.deps.Now().In(siteLocation(site)).Format(time.DateOnly))
	if err != nil {
		serviceError(w, err)
		return
	}
	out := make([]pixelJSON, 0, len(rows))
	for _, p := range rows {
		out = append(out, h.toPixelJSON(r, p, v30))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) createPixel(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.pixelSite(w, r, authz.ActionLinksManage)
	if !ok {
		return
	}
	var in pixelInput
	if !decodeJSON(w, r, &in) {
		return
	}
	p := pixels.Pixel{SiteID: site.ID, Enabled: true}
	if err := applyPixelInput(&p, in); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
		return
	}
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		var err error
		if p, err = pixels.Create(r.Context(), tx, p); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "pixel.created", "pixel", p.ID, map[string]any{"site_id": site.ID})
	})
	if errors.Is(err, pixels.ErrLimit) {
		writeProblem(w, http.StatusConflict, "Conflict", "a project can have at most 100 pixels")
		return
	}
	if err != nil {
		serviceError(w, err)
		return
	}
	h.reloadPixels(r)
	writeJSON(w, http.StatusCreated, h.toPixelJSON(r, p, nil))
}

func (h *handler) updatePixel(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.pixelSite(w, r, authz.ActionLinksManage)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeProblem(w, http.StatusBadRequest, "Bad request", "id must be a positive integer")
		return
	}
	var in pixelInput
	if !decodeJSON(w, r, &in) {
		return
	}
	var p pixels.Pixel
	err = h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		list, err := pixels.List(r.Context(), tx, site.ID)
		if err != nil {
			return err
		}
		for _, candidate := range list {
			if candidate.ID == id {
				p = candidate
			}
		}
		if p.ID == 0 {
			return pixels.ErrNotFound
		}
		if err := applyPixelInput(&p, in); err != nil {
			return errBadPixel{err}
		}
		if p, err = pixels.Update(r.Context(), tx, p); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "pixel.updated", "pixel", p.ID, map[string]any{"site_id": site.ID})
	})
	var bad errBadPixel
	switch {
	case errors.As(err, &bad):
		writeProblem(w, http.StatusBadRequest, "Bad request", bad.Error())
	case errors.Is(err, pixels.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "pixel not found")
	case err != nil:
		serviceError(w, err)
	default:
		h.reloadPixels(r)
		writeJSON(w, http.StatusOK, h.toPixelJSON(r, p, nil))
	}
}

type errBadPixel struct{ error }

func (h *handler) deletePixel(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.pixelSite(w, r, authz.ActionLinksManage)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeProblem(w, http.StatusBadRequest, "Bad request", "id must be a positive integer")
		return
	}
	err = h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		if err := pixels.Delete(r.Context(), tx, site.ID, id); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "pixel.deleted", "pixel", id, map[string]any{"site_id": site.ID})
	})
	if errors.Is(err, pixels.ErrNotFound) {
		writeProblem(w, http.StatusNotFound, "Not found", "pixel not found")
		return
	}
	if err != nil {
		serviceError(w, err)
		return
	}
	h.reloadPixels(r)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) pixelStats(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.pixelSite(w, r, authz.ActionStatsRead)
	if !ok {
		return
	}
	from, to, ok := statsRange(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	var pixelID int64
	if raw := r.URL.Query().Get("pixel"); raw != "" {
		var err error
		if pixelID, err = strconv.ParseInt(raw, 10, 64); err != nil || pixelID <= 0 {
			writeProblem(w, http.StatusBadRequest, "Bad request", "pixel must be a positive integer")
			return
		}
	}
	stats, err := pixels.StatsFor(r.Context(), h.deps.Store.Pool, site.ID, from, to, pixelID)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "daily": stats.Daily, "pixels": stats.Pixels, "countries": stats.Countries})
}

func (h *handler) reloadPixels(r *http.Request) {
	if err := h.deps.Pixels.Reload(r.Context()); err != nil {
		h.logger().Warn("reload pixel registry", "err", err)
	}
}

func siteLocation(site coresites.Site) *time.Location {
	if site.TZ == nil {
		return time.UTC
	}
	return site.TZ
}
```

Adjust import paths for `audit`/`authz` and the logger accessor to what `handlers_links.go` uses (copy its import block). Register routes in `private.go` next to the link routes:

```go
	mux.HandleFunc("GET /api/private/sites/{site}/pixels", h.listPixels)
	mux.HandleFunc("POST /api/private/sites/{site}/pixels", h.createPixel)
	mux.HandleFunc("GET /api/private/sites/{site}/pixels/stats", h.pixelStats)
	mux.HandleFunc("PATCH /api/private/sites/{site}/pixels/{id}", h.updatePixel)
	mux.HandleFunc("DELETE /api/private/sites/{site}/pixels/{id}", h.deletePixel)
```

Add `Pixels *pixels.Registry` to `Deps`, and `Pixels: pixelRegistry` in `serve.go`.

- [ ] **Step 8: Run and commit the API**

Run: `go build ./... && go vet ./... && go test ./internal/platform/http -count=1`
Expected: `ok`.

```bash
git add internal/platform/http cmd/supermetry/serve.go
git commit -m "feat(api): manage pixels and read pixel stats"
```

- [ ] **Step 9: Document in OpenAPI**

Add under `paths` (follow the style of `/api/private/sites/{site}/links`, same security and `Problem` responses):

```yaml
  /api/private/sites/{site}/pixels:
    parameters: [{ $ref: '#/components/parameters/SitePath' }]
    get:
      operationId: listPixels
      tags: [Pixels]
      responses:
        '200': { description: Pixels, content: { application/json: { schema: { type: array, items: { $ref: '#/components/schemas/Pixel' } } } } }
        '404': { $ref: '#/components/responses/Problem' }
    post:
      operationId: createPixel
      tags: [Pixels]
      requestBody: { required: true, content: { application/json: { schema: { $ref: '#/components/schemas/PixelInput' } } } }
      responses:
        '201': { description: Created, content: { application/json: { schema: { $ref: '#/components/schemas/Pixel' } } } }
        '400': { $ref: '#/components/responses/Problem' }
        '409': { description: The project already has 100 pixels, content: { application/problem+json: { schema: { $ref: '#/components/schemas/Problem' } } } }
  /api/private/sites/{site}/pixels/stats:
    parameters: [{ $ref: '#/components/parameters/SitePath' }]
    get:
      operationId: pixelStats
      tags: [Pixels]
      parameters:
        - { name: from, in: query, schema: { type: string, format: date } }
        - { name: to, in: query, schema: { type: string, format: date } }
        - { name: pixel, in: query, schema: { type: integer, format: int64, minimum: 1 } }
      responses:
        '200': { description: Views, content: { application/json: { schema: { $ref: '#/components/schemas/PixelStats' } } } }
  /api/private/sites/{site}/pixels/{id}:
    parameters:
      - { $ref: '#/components/parameters/SitePath' }
      - { name: id, in: path, required: true, schema: { type: integer, format: int64 } }
    patch:
      operationId: updatePixel
      tags: [Pixels]
      requestBody: { required: true, content: { application/json: { schema: { $ref: '#/components/schemas/PixelInput' } } } }
      responses:
        '200': { description: Updated, content: { application/json: { schema: { $ref: '#/components/schemas/Pixel' } } } }
        '404': { $ref: '#/components/responses/Problem' }
    delete:
      operationId: deletePixel
      tags: [Pixels]
      responses:
        '204': { description: Deleted with its daily views }
        '404': { $ref: '#/components/responses/Problem' }
```

and schemas:

```yaml
    PixelInput:
      type: object
      additionalProperties: false
      properties:
        name: { type: string, minLength: 1, maxLength: 120 }
        badge_label: { type: string, maxLength: 40 }
        enabled: { type: boolean }
    Pixel:
      type: object
      required: [id, public_id, name, badge_label, enabled, created_at, total, views_30d, gif_url, svg_url]
      properties:
        id: { type: integer, format: int64 }
        public_id: { type: string, pattern: '^p[A-Za-z0-9]{11}$' }
        name: { type: string }
        badge_label: { type: string }
        enabled: { type: boolean }
        created_at: { type: string, format: date-time }
        total: { type: integer, format: int64, description: All-time views (views, not unique visitors). }
        views_30d: { type: integer, format: int64 }
        gif_url: { type: string, format: uri }
        svg_url: { type: string, format: uri }
    PixelStats:
      type: object
      required: [from, to, daily, pixels, countries]
      properties:
        from: { type: string, format: date }
        to: { type: string, format: date }
        daily:
          type: array
          items: { type: object, required: [day, proxy, direct], properties: { day: { type: string, format: date }, proxy: { type: integer }, direct: { type: integer } } }
        pixels:
          type: array
          items: { type: object, required: [pixel_id, views], properties: { pixel_id: { type: integer, format: int64 }, views: { type: integer } } }
        countries:
          type: array
          description: Direct views only; image proxies such as GitHub hide the country.
          items: { type: object, required: [country, views], properties: { country: { type: string }, views: { type: integer } } }
```

Use the parameter/response component names that already exist (`grep -n "parameters:\|  responses:" internal/api/openapi.yaml | head`) if `SitePath`/`Problem` are spelled differently. Also document `GET /px/{file}` (public, image/gif and image/svg+xml, `style`, `color` query) next to the existing pixel path entry.

- [ ] **Step 10: Verify and commit OpenAPI + generated client**

Run: `go test ./internal/api -count=1 && make api-inventory && cd web && bun run gen:api && bun run typecheck`
Expected: `ok`; inventory reports no undocumented routes; typecheck exit 0.

```bash
git add internal/api/openapi.yaml
git commit -m "docs(api): document pixel endpoints"
git add web/src/lib/api.gen.ts web/packages/api-client/src/schema.ts
git commit -m "chore(web): regenerate API client for pixels"
```

### Task 9: Retention for pixel views

**Files:**
- Modify: `internal/retention/retention.go` (new `deleteExpiredPixelViews`, called from `Run`; add `pixel_views_daily` handling to `Erase` via the pixels join)
- Test: `internal/retention/retention_test.go`

Decision: the spec says pixel rows follow the project's retention class "using the same purge job as other daily tables". `pixel_views_daily` is small and unpartitioned, so the job deletes rows older than `retention_class + 1` days in the site's zone, the same one day of closing slack `Run` gives partitions (`retention.go:57`: `cutoff := now.AddDate(0, 0, -int(partition.Class)-1)`), so class `0` keeps about one day.

**Interfaces:**
- Consumes: `retention.Run(ctx, pool, now)`.
- Produces: `func deleteExpiredPixelViews(ctx, pool, now) error`.

- [ ] **Step 1: Write the failing test**

```go
func TestRunDeletesExpiredPixelViews(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	_ = pool.QueryRow(ctx, `insert into orgs(name,slug) values ('R','r-px') returning id`).Scan(&orgID)
	var siteID int32
	_ = pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,kind) values ($1,'retpx0000001','R','UTC',30,'pixel') returning id`, orgID).Scan(&siteID)
	var pixelID int64
	_ = pool.QueryRow(ctx, `insert into pixels(site_id,public_id,name) values ($1,'pRETENTION00','r') returning id`, siteID).Scan(&pixelID)
	if _, err := pool.Exec(ctx, `insert into pixel_views_daily values ($1,'2026-08-01','','proxy',1),($1,'2026-10-10','','proxy',2)`, pixelID); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, pool, time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	var left int64
	_ = pool.QueryRow(ctx, `select coalesce(sum(views),0) from pixel_views_daily`).Scan(&left)
	if left != 2 {
		t.Fatalf("left = %d, want only the recent row (2)", left)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/retention -run TestRunDeletesExpiredPixelViews -count=1`
Expected: FAIL, `left = 3`.

- [ ] **Step 3: Implement**

```go
// deleteExpiredPixelViews applies each pixel project's retention class to its
// small, unpartitioned daily table, in the project's time zone.
func deleteExpiredPixelViews(ctx context.Context, pool *pgxpool.Pool, now time.Time) error {
	_, err := pool.Exec(ctx, `
		delete from pixel_views_daily v using pixels p, sites s
		where v.pixel_id = p.id and p.site_id = s.id
		  and v.day < (($1::timestamptz at time zone s.timezone)::date - s.retention_class - 1)`, now)
	if err != nil {
		return fmt.Errorf("delete expired pixel views: %w", err)
	}
	return nil
}
```

Call it from `Run` right after `deleteExpiredDropStats`. A bad `timezone` string would fail the statement; sites are validated on write, and the same assumption holds for `at time zone` elsewhere — if `Run` must survive bad zones, switch to the per-site loop used by `deleteExpiredDropStats`.

- [ ] **Step 4: Run tests and commit**

Run: `go vet ./internal/retention && go test ./internal/retention -count=1`
Expected: `ok`.

```bash
git add internal/retention/retention.go internal/retention/retention_test.go
git commit -m "feat(retention): expire pixel views by retention class"
```

### Task 10: Open counters schema, config and key parsing

**Files:**
- Create: `internal/db/migrations/0170_open_counters.sql`, `internal/db/migrate_open_counters_test.go`
- Create: `internal/opencounters/key.go`, `internal/opencounters/key_test.go`
- Modify: `internal/config/config.go`, `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - tables `open_counters(id bigint identity, key text unique, created_at, last_seen_day date)`, `open_counter_views_daily(counter_id, day, views)`
  - `config.Config.OpenCounters bool` (`SUPERMETRY_OPEN_COUNTERS`, default `false`), `config.Config.OpenCounterBlocklist string` (`SUPERMETRY_OPEN_COUNTER_BLOCKLIST`, a file path, optional)
  - `func opencounters.ParsePath(rest string) (key, ext string, ok bool)` — `rest` is everything after `/c/`; returns key like `github.com/owner` or `github.com/owner/repo` (lower-cased) and ext `"svg"|"gif"|""`
  - `func opencounters.LoadBlocklist(path string) (map[string]bool, error)` — one key per line, `#` comments, blank lines ignored, keys lower-cased

- [ ] **Step 1: Write the failing tests**

`key_test.go`:

```go
package opencounters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePath(t *testing.T) {
	ok := map[string][2]string{
		"github.com/Velixio.svg":            {"github.com/velixio", "svg"},
		"github.com/velixio/Supermetry.gif": {"github.com/velixio/supermetry", "gif"},
		"github.com/a-b/c.d_e-f":            {"github.com/a-b/c.d_e-f", ""},
		"github.com/x/" + strings.Repeat("r", 100) + ".svg": {"github.com/x/" + strings.Repeat("r", 100), "svg"},
	}
	for in, want := range ok {
		key, ext, good := ParsePath(in)
		if !good || key != want[0] || ext != want[1] {
			t.Errorf("ParsePath(%q) = %q %q %v", in, key, ext, good)
		}
	}
	for _, bad := range []string{
		"gitlab.com/a.svg", "github.com/.svg", "github.com/a_b.svg", "github.com/" + strings.Repeat("o", 40) + ".svg",
		"github.com/a/" + strings.Repeat("r", 101) + ".svg", "github.com/a/b/c.svg", "github.com/a/<x>.svg",
		"github.com/a.png", "github.com/a/b.svg.svg", "github.com/a/..svg",
	} {
		if _, _, good := ParsePath(bad); good {
			t.Errorf("ParsePath(%q) accepted", bad)
		}
	}
}

func TestLoadBlocklist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "block.txt")
	_ = os.WriteFile(path, []byte("# spam\nGitHub.com/Bad\n\n github.com/bad/repo \n"), 0o600)
	list, err := LoadBlocklist(path)
	if err != nil || !list["github.com/bad"] || !list["github.com/bad/repo"] || len(list) != 2 {
		t.Fatalf("list = %v, %v", list, err)
	}
	if empty, err := LoadBlocklist(""); err != nil || len(empty) != 0 {
		t.Fatalf("empty = %v, %v", empty, err)
	}
}
```

Note `github.com/a/..svg`: the repo would be `.`, which GitHub forbids; the parser rejects repos `.` and `..`.

`migrate_open_counters_test.go`:

```go
package db_test

import (
	"context"
	"testing"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestOpenCountersMigration(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := pool.QueryRow(ctx, `insert into open_counters(key,last_seen_day) values ('github.com/a','2026-10-14') returning id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into open_counters(key,last_seen_day) values ('github.com/a','2026-10-14')`); err == nil {
		t.Fatal("duplicate key accepted")
	}
	if _, err := pool.Exec(ctx, `insert into open_counters(key,last_seen_day) values ('GitHub.com/A','2026-10-14')`); err == nil {
		t.Fatal("upper-case key accepted")
	}
	if _, err := pool.Exec(ctx, `insert into open_counter_views_daily values ($1,'2026-10-14',3)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `delete from open_counters where id=$1`, id); err != nil {
		t.Fatal(err)
	}
}
```

Config test (same style as Task 6):

```go
func TestLoadOpenCounters(t *testing.T) {
	values := baseValues() // match the helper name used by TestLoadHomepageURL
	c, err := Load(mapGetenv(values))
	if err != nil || c.OpenCounters || c.OpenCounterBlocklist != "" {
		t.Fatalf("defaults = %v %q %v", c.OpenCounters, c.OpenCounterBlocklist, err)
	}
	values["SUPERMETRY_OPEN_COUNTERS"] = "true"
	values["SUPERMETRY_OPEN_COUNTER_BLOCKLIST"] = "/etc/supermetry/blocklist.txt"
	c, err = Load(mapGetenv(values))
	if err != nil || !c.OpenCounters || c.OpenCounterBlocklist != "/etc/supermetry/blocklist.txt" {
		t.Fatalf("set = %v %q %v", c.OpenCounters, c.OpenCounterBlocklist, err)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/db ./internal/opencounters ./internal/config -count=1`
Expected: FAIL (`relation "open_counters" does not exist`, `undefined: ParsePath`, `c.OpenCounters undefined`).

- [ ] **Step 3: Migration `0170_open_counters.sql`**

```sql
-- Open counters: account-free view counters for GitHub profiles and repos.
-- Only aggregate daily views; no IPs, user agents or countries.
-- Small and unpartitioned: cleanup uses DELETE (documented exception).

CREATE TABLE open_counters (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    key text NOT NULL UNIQUE CHECK (key = lower(key) AND char_length(key) <= 160),
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_day date NOT NULL
);
CREATE INDEX open_counters_last_seen_day_idx ON open_counters (last_seen_day);

CREATE TABLE open_counter_views_daily (
    counter_id bigint NOT NULL REFERENCES open_counters(id) ON DELETE CASCADE,
    day date NOT NULL,
    views bigint NOT NULL CHECK (views > 0),
    PRIMARY KEY (counter_id, day)
);
CREATE INDEX open_counter_views_daily_day_idx ON open_counter_views_daily USING brin (day);
```

The `last_seen_day` btree serves the 365-day idle cleanup; BRIN on `day` serves the 400-day delete.

- [ ] **Step 4: `key.go`**

```go
// Package opencounters serves account-free view counters for GitHub keys.
package opencounters

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

var (
	ownerRE = regexp.MustCompile(`^[A-Za-z0-9-]{1,39}$`)
	repoRE  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

const host = "github.com"

func ParsePath(rest string) (key, ext string, ok bool) {
	for _, e := range []string{".svg", ".gif"} {
		if trimmed, found := strings.CutSuffix(rest, e); found {
			rest, ext = trimmed, e[1:]
			break
		}
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || len(parts) > 3 || parts[0] != host || !ownerRE.MatchString(parts[1]) {
		return "", "", false
	}
	if len(parts) == 3 && (!repoRE.MatchString(parts[2]) || parts[2] == "." || parts[2] == ".." || strings.HasSuffix(parts[2], ".svg") || strings.HasSuffix(parts[2], ".gif")) {
		return "", "", false
	}
	if len(parts) == 2 && strings.Contains(parts[1], ".") {
		return "", "", false
	}
	return strings.ToLower(strings.Join(parts, "/")), ext, true
}

func LoadBlocklist(path string) (map[string]bool, error) {
	out := map[string]bool{}
	if path == "" {
		return out, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if line != "" && !strings.HasPrefix(line, "#") {
			out[line] = true
		}
	}
	return out, scanner.Err()
}
```

(`github.com/a.png` is rejected because the owner part `a.png` contains a dot, which the owner regexp disallows.)

- [ ] **Step 5: Config**

Add `OpenCounters bool` and `OpenCounterBlocklist string` to `Config`; in `Load`: `OpenCounterBlocklist: getenv("SUPERMETRY_OPEN_COUNTER_BLOCKLIST"),` in the literal and `parseOptionalBool(getenv("SUPERMETRY_OPEN_COUNTERS"), "SUPERMETRY_OPEN_COUNTERS", &c.OpenCounters, &errs)` after the badge line.

- [ ] **Step 6: Run and commit (three commits)**

Run: `go vet ./... && go test ./internal/db ./internal/opencounters ./internal/config -count=1`
Expected: all `ok`.

```bash
git add internal/db/migrations/0170_open_counters.sql internal/db/migrate_open_counters_test.go
git commit -m "feat(db): add open counter tables"
git add internal/opencounters/key.go internal/opencounters/key_test.go
git commit -m "feat(opencounters): parse GitHub counter keys and blocklist"
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add open counter switch and blocklist path"
```

### Task 11: Open counter recorder and badge handler

**Files:**
- Create: `internal/opencounters/recorder.go`, `internal/opencounters/recorder_test.go`, `internal/opencounters/handler.go`, `internal/opencounters/handler_test.go`

**Interfaces:**
- Consumes: `pixels.DetectProxy`, `pixels.NewProxyLimiter`/`Allow(int64, string)`, `pixels.RenderBadge`, `pixels.ParseBadgeQuery`, `links.TransparentGIF`, `ingest.NewTokenBuckets`, `ParsePath`.
- Produces:
  - `type Store interface { Upsert(ctx, rows map[ViewKey]int64) error; Totals(ctx, keys []string) (map[string]int64, error) }`; `PoolStore(pool) Store`
  - `type ViewKey struct { Key, Day string }`
  - `type Recorder struct`; `NewRecorder(store Store, newPerMinute float64, newBurst int) *Recorder`; `View(key, day string)`; `Total(key string) int64` (cache only; marks cold keys for the next flush); `Flush(ctx) error`; `Run(ctx) error`
  - `type Handler struct { Recorder *Recorder; Proxy *pixels.ProxyLimiter; Blocklist map[string]bool; Branded bool; HomepageURL string; Now func() time.Time; Stats StatsFunc }`; `ServeHTTP` for `GET /c/{rest...}`

Decision: totals for counters live in a `map[string]int64` inside the recorder (keyed by counter key, since a brand-new counter has no id until flush) rather than `pixels.TotalCache` (keyed by pixel id); behaviour matches the spec's "same in-memory total cache". The proxy limiter key is the FNV-64a hash of the counter key.

Counter-creation cap semantics: a key not yet known to exist (not in the in-memory known set) needs a token from the global new-counter limiter (50/min, burst 500) on its first view; without one the view is not counted. Keys loaded from the DB or already created are "known" and skip the cap.

- [ ] **Step 1: Write the failing recorder test**

```go
package opencounters

import (
	"context"
	"maps"
	"sync"
	"testing"
)

type memStore struct {
	mu     sync.Mutex
	views  map[ViewKey]int64
	totals map[string]int64
	loads  int
}

func (m *memStore) Upsert(_ context.Context, rows map[ViewKey]int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, n := range rows {
		m.views[k] += n
	}
	return nil
}

func (m *memStore) Totals(_ context.Context, keys []string) (map[string]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loads++
	out := map[string]int64{}
	for _, k := range keys {
		if n, ok := m.totals[k]; ok {
			out[k] = n
		}
	}
	return out, nil
}

func TestColdKeyLoadsLazilyOnFlush(t *testing.T) {
	store := &memStore{views: map[ViewKey]int64{}, totals: map[string]int64{"github.com/old": 500}}
	r := NewRecorder(store, 50, 500)
	if got := r.Total("github.com/old"); got != 0 {
		t.Fatalf("before load = %d, want 0 (no DB read on request)", got)
	}
	if err := r.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.View("github.com/old", "2026-10-14")
	if got := r.Total("github.com/old"); got != 501 || store.loads != 1 {
		t.Fatalf("total = %d loads = %d", got, store.loads)
	}
}

func TestNewCounterCap(t *testing.T) {
	store := &memStore{views: map[ViewKey]int64{}, totals: map[string]int64{}}
	r := NewRecorder(store, 0.0001, 2)
	for _, k := range []string{"github.com/a", "github.com/b", "github.com/c"} {
		r.View(k, "2026-10-14")
	}
	r.View("github.com/a", "2026-10-14") // known now; no new token needed
	_ = r.Flush(context.Background())
	want := map[ViewKey]int64{{"github.com/a", "2026-10-14"}: 2, {"github.com/b", "2026-10-14"}: 1}
	if !maps.Equal(store.views, want) {
		t.Fatalf("views = %v", store.views)
	}
}
```

Add a DB round trip to `recorder_test.go` (needs `SUPERMETRY_TEST_DSN`):

```go
func TestPoolStoreCreatesCountersAndSumsTotals(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := PoolStore(pool)
	for range 2 {
		if err := s.Upsert(ctx, map[ViewKey]int64{{"github.com/a", "2026-10-13"}: 2, {"github.com/a", "2026-10-14"}: 1}); err != nil {
			t.Fatal(err)
		}
	}
	totals, err := s.Totals(ctx, []string{"github.com/a", "github.com/missing"})
	if err != nil || totals["github.com/a"] != 6 || len(totals) != 1 {
		t.Fatalf("totals = %v, %v", totals, err)
	}
	var lastSeen string
	_ = pool.QueryRow(ctx, `select last_seen_day::text from open_counters where key='github.com/a'`).Scan(&lastSeen)
	if lastSeen != "2026-10-14" {
		t.Fatalf("last_seen_day = %s", lastSeen)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/opencounters -count=1`
Expected: FAIL to compile, `undefined: NewRecorder`.

- [ ] **Step 3: Implement `recorder.go`**

```go
package opencounters

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/ingest"
)

const maxKeys = 200_000

type ViewKey struct{ Key, Day string }

type Store interface {
	Upsert(ctx context.Context, rows map[ViewKey]int64) error
	Totals(ctx context.Context, keys []string) (map[string]int64, error)
}

type Recorder struct {
	store   Store
	newCap  ingest.RateLimiter
	mu      sync.Mutex
	rows    map[ViewKey]int64
	totals  map[string]int64
	known   map[string]bool // exists in DB or created this run
	cold    map[string]bool // requested but not yet loaded
	flushMu sync.Mutex
}

func NewRecorder(store Store, newPerMinute float64, newBurst int) *Recorder {
	return &Recorder{
		store: store, newCap: ingest.NewTokenBuckets(newPerMinute/60, newBurst),
		rows: map[ViewKey]int64{}, totals: map[string]int64{}, known: map[string]bool{}, cold: map[string]bool{},
	}
}

// Total returns the cached total; an unseen key is queued for the next flush.
func (r *Recorder) Total(key string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.totals[key]; !ok && !r.known[key] && len(r.cold) < maxKeys {
		r.cold[key] = true
	}
	return r.totals[key]
}

func (r *Recorder) View(key, day string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.known[key] {
		if len(r.known) >= maxKeys || !r.newCap.Allow("new") {
			return
		}
		r.known[key] = true
		delete(r.cold, key)
	}
	r.rows[ViewKey{key, day}]++
	r.totals[key]++
}

func (r *Recorder) Flush(ctx context.Context) error {
	r.flushMu.Lock()
	defer r.flushMu.Unlock()
	r.mu.Lock()
	rows, cold := r.rows, make([]string, 0, len(r.cold))
	r.rows = map[ViewKey]int64{}
	for k := range r.cold {
		cold = append(cold, k)
	}
	r.cold = map[string]bool{}
	r.mu.Unlock()

	if len(rows) > 0 {
		if err := r.store.Upsert(ctx, rows); err != nil {
			r.mu.Lock()
			for k, n := range rows {
				r.rows[k] += n
			}
			r.mu.Unlock()
			return err
		}
	}
	if len(cold) > 0 {
		loaded, err := r.store.Totals(ctx, cold)
		if err != nil {
			return err
		}
		r.mu.Lock()
		for k, n := range loaded {
			r.totals[k] += n // views recorded before the load are added on top
			r.known[k] = true
		}
		r.mu.Unlock()
	}
	return nil
}

func (r *Recorder) Run(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return r.Flush(flushCtx)
		case <-ticker.C:
			if err := r.Flush(ctx); err != nil {
				slog.Warn("open counter flush failed", "err", err)
			}
		}
	}
}

type poolStore struct{ pool *pgxpool.Pool }

func PoolStore(pool *pgxpool.Pool) Store { return poolStore{pool} }

func (s poolStore) Upsert(ctx context.Context, rows map[ViewKey]int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		batch := &pgx.Batch{}
		for k, n := range rows {
			batch.Queue(`
				with c as (
					insert into open_counters(key, last_seen_day) values ($1, $2::date)
					on conflict (key) do update set last_seen_day = greatest(open_counters.last_seen_day, excluded.last_seen_day)
					returning id)
				insert into open_counter_views_daily(counter_id, day, views) select id, $2::date, $3 from c
				on conflict (counter_id, day) do update set views = open_counter_views_daily.views + excluded.views`,
				k.Key, k.Day, n)
		}
		return tx.SendBatch(ctx, batch).Close()
	})
}

func (s poolStore) Totals(ctx context.Context, keys []string) (map[string]int64, error) {
	rows, err := s.pool.Query(ctx, `
		select c.key, coalesce(sum(v.views), 0)::bigint from open_counters c
		left join open_counter_views_daily v on v.counter_id = c.id
		where c.key = any($1) group by c.key`, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}
```

Known edge: a key that exists in the DB but has not been loaded yet is treated as new on its first view, and spends a new-counter token. That is acceptable (the cap only bounds the creation rate) and keeps the hot path DB-free.

- [ ] **Step 4: Run and commit the recorder**

Run: `go test -race ./internal/opencounters -count=1`
Expected: `ok`.

```bash
git add internal/opencounters/recorder.go internal/opencounters/recorder_test.go
git commit -m "feat(opencounters): buffer counter views with lazy totals"
```

- [ ] **Step 5: Write the failing handler test** (`handler_test.go`)

```go
package opencounters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/pixels"
)

func newCounterServer(t *testing.T, enabled bool) (*http.ServeMux, *memStore, *Handler) {
	t.Helper()
	store := &memStore{views: map[ViewKey]int64{}, totals: map[string]int64{}}
	h := &Handler{
		Recorder: NewRecorder(store, 50, 500), Proxy: pixels.NewProxyLimiter(),
		Blocklist: map[string]bool{"github.com/spam": true}, Branded: true, HomepageURL: "https://supermetry.example",
		Now: func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) },
	}
	mux := http.NewServeMux()
	Register(mux, h, enabled)
	return mux, store, h
}

func hit(mux *http.ServeMux, path, ua string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("User-Agent", ua)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestDisabledReturns404(t *testing.T) {
	mux, _, _ := newCounterServer(t, false)
	if res := hit(mux, "/c/github.com/a.svg", "github-camo (1)"); res.Code != http.StatusNotFound {
		t.Fatalf("status = %d", res.Code)
	}
}

func TestOnlyProxiedViewsCount(t *testing.T) {
	mux, store, h := newCounterServer(t, true)
	for _, tc := range []struct{ path, ua string }{
		{"/c/github.com/Velixio/Supermetry.svg", "github-camo (1)"},
		{"/c/github.com/velixio/supermetry.gif", "github-camo (1)"},
		{"/c/github.com/velixio/supermetry.svg", "curl/8.5.0"},
		{"/c/github.com/spam.svg", "github-camo (1)"},
	} {
		res := hit(mux, tc.path, tc.ua)
		if res.Code != 200 || res.Header().Get("Cache-Control") != "no-store, max-age=0" {
			t.Fatalf("%s: %d %v", tc.path, res.Code, res.Header())
		}
	}
	if res := hit(mux, "/c/github.com/bad_owner.svg", "github-camo (1)"); res.Code != http.StatusNotFound {
		t.Fatalf("invalid key status = %d", res.Code)
	}
	if err := h.Recorder.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.views[ViewKey{"github.com/velixio/supermetry", "2026-10-14"}] != 2 || len(store.views) != 1 {
		t.Fatalf("views = %v", store.views)
	}
	svg := hit(mux, "/c/github.com/velixio/supermetry.svg", "curl/8.5.0").Body.String()
	if !strings.Contains(svg, ">views<") || !strings.Contains(svg, ">2<") {
		t.Fatalf("badge = %s", svg)
	}
}
```

- [ ] **Step 6: Implement `handler.go`**

```go
package opencounters

import (
	"hash/fnv"
	"net/http"
	"strconv"
	"time"

	"supermetry/internal/links"
	"supermetry/internal/pixels"
)

type Handler struct {
	Recorder    *Recorder
	Proxy       *pixels.ProxyLimiter
	Blocklist   map[string]bool
	Branded     bool
	HomepageURL string
	Now         func() time.Time
	Stats       StatsFunc // Task 12; nil serves 404 for the page
}

// Register mounts /c/ routes; when disabled every /c/ path is 404.
func Register(mux *http.ServeMux, h *Handler, enabled bool) {
	if !enabled {
		mux.HandleFunc("GET /c/", http.NotFound)
		return
	}
	mux.Handle("GET /c/{rest...}", h)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key, ext, ok := ParsePath(r.PathValue("rest"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if ext == "" {
		h.servePage(w, r, key)
		return
	}
	if !h.Blocklist[key] && r.Method == http.MethodGet {
		if proxy, isProxy := pixels.DetectProxy(r.UserAgent()); isProxy && h.Proxy.Allow(hashKey(key), proxy) {
			h.Recorder.View(key, h.Now().UTC().Format(time.DateOnly))
		}
	}
	var body []byte
	contentType := "image/gif"
	if ext == "svg" {
		total := int64(0)
		if !h.Blocklist[key] {
			total = h.Recorder.Total(key)
		}
		style, color := pixels.ParseBadgeQuery(r.URL.Query())
		body, contentType = pixels.RenderBadge(pixels.BadgeOptions{Label: "views", Count: total, Style: style, Color: color, Branded: h.Branded}), "image/svg+xml"
	} else {
		body = links.TransparentGIF
	}
	header := w.Header()
	header.Set("Content-Type", contentType)
	header.Set("Cache-Control", "no-store, max-age=0")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func hashKey(key string) int64 {
	f := fnv.New64a()
	_, _ = f.Write([]byte(key))
	return int64(f.Sum64() >> 1)
}
```

Days are UTC (counters have no project time zone). Until Task 12 lands, add a temporary `func (h *Handler) servePage(w http.ResponseWriter, r *http.Request, _ string) { http.NotFound(w, r) }` and `type StatsFunc func()` in `page.go` so this commit builds; Task 12 replaces both.

- [ ] **Step 7: Run and commit the handler**

Run: `go vet ./internal/opencounters && go test -race ./internal/opencounters -count=1`
Expected: `ok`.

```bash
git add internal/opencounters/handler.go internal/opencounters/handler_test.go internal/opencounters/page.go
git commit -m "feat(opencounters): serve counter badges for proxied views"
```

### Task 12: Open counter stats page, cleanup job and server wiring

**Files:**
- Modify: `internal/opencounters/page.go` (real page), Create: `internal/opencounters/page_test.go`
- Create: `internal/opencounters/cleanup.go`, `internal/opencounters/cleanup_test.go`
- Modify: `internal/api/router.go` (`OpenCounters *opencounters.Handler`, `OpenCountersEnabled bool`; call `opencounters.Register`)
- Modify: `cmd/supermetry/serve.go` (build recorder/handler when `cfg.OpenCounters`; run recorder; add daily `open-counters-cleanup` job next to `dict-gc` at line ~991)

**Interfaces:**
- Produces:
  - `type DayCount struct { Day string; Views int64 }`
  - `type StatsFunc func(ctx context.Context, key string, from, to string) ([]DayCount, error)`; `PoolStats(pool) StatsFunc`
  - `func RenderSparkline(days []DayCount, width, height int) string` (inline SVG `<polyline>`)
  - `func Cleanup(ctx, pool *pgxpool.Pool, today time.Time) (deletedCounters int64, err error)` — deletes counters with `last_seen_day < today-365` and daily rows with `day < today-400`

- [ ] **Step 1: Write the failing tests**

`page_test.go`:

```go
package opencounters

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestStatsPage(t *testing.T) {
	mux, _, h := newCounterServer(t, true)
	h.Stats = func(_ context.Context, key, from, to string) ([]DayCount, error) {
		if key != "github.com/velixio" || from != "2026-09-15" || to != "2026-10-14" {
			t.Errorf("stats args %s %s %s", key, from, to)
		}
		return []DayCount{{"2026-10-13", 3}, {"2026-10-14", 5}}, nil
	}
	res := hit(mux, "/c/github.com/Velixio", "Mozilla/5.0")
	body := res.Body.String()
	if res.Code != 200 || !strings.HasPrefix(res.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("%d %v", res.Code, res.Header())
	}
	for _, want := range []string{"github.com/velixio", "<polyline", "Track your own site with Supermetry", `href="https://supermetry.example/?ref=counter"`, "views, not unique visitors"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(body, "<script") {
		t.Error("page must not ship scripts")
	}
	if res := hit(mux, "/c/github.com/spam", "Mozilla/5.0"); res.Code != http.StatusNotFound {
		t.Errorf("blocked key page = %d", res.Code)
	}
}

func TestRenderSparklineEmptyAndFlat(t *testing.T) {
	if s := RenderSparkline(nil, 300, 40); !strings.Contains(s, "<svg") {
		t.Fatalf("empty = %s", s)
	}
	if s := RenderSparkline([]DayCount{{"2026-10-14", 0}}, 300, 40); strings.Contains(s, "NaN") {
		t.Fatalf("flat = %s", s)
	}
}
```

`cleanup_test.go`:

```go
package opencounters

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestCleanupDeletesIdleCountersAndOldRows(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var idle, live int64
	_ = pool.QueryRow(ctx, `insert into open_counters(key,last_seen_day) values ('github.com/idle','2025-10-13') returning id`).Scan(&idle)
	_ = pool.QueryRow(ctx, `insert into open_counters(key,last_seen_day) values ('github.com/live','2026-10-14') returning id`).Scan(&live)
	_, _ = pool.Exec(ctx, `insert into open_counter_views_daily values ($1,'2025-10-13',1),($2,'2025-09-01',7),($2,'2026-10-14',2)`, idle, live)
	n, err := Cleanup(ctx, pool, time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC))
	if err != nil || n != 1 {
		t.Fatalf("deleted = %d, %v", n, err)
	}
	var rows int64
	_ = pool.QueryRow(ctx, `select coalesce(sum(views),0) from open_counter_views_daily`).Scan(&rows)
	if rows != 2 {
		t.Fatalf("remaining views = %d, want 2", rows)
	}
}
```

(2025-10-13 is 366 days before 2026-10-14, so `idle` goes; 2025-09-01 is 408 days old, so that row goes.)

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/opencounters -run 'StatsPage|Sparkline|Cleanup' -count=1`
Expected: FAIL to compile, `undefined: DayCount`.

- [ ] **Step 3: Implement `page.go`** (replace the Task 11 stub)

```go
package opencounters

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/pixels"
)

type DayCount struct {
	Day   string
	Views int64
}

type StatsFunc func(ctx context.Context, key, from, to string) ([]DayCount, error)

func PoolStats(pool *pgxpool.Pool) StatsFunc {
	return func(ctx context.Context, key, from, to string) ([]DayCount, error) {
		rows, err := pool.Query(ctx, `select v.day::text, v.views from open_counter_views_daily v
			join open_counters c on c.id = v.counter_id where c.key=$1 and v.day between $2::date and $3::date order by v.day`, key, from, to)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []DayCount
		for rows.Next() {
			var d DayCount
			if err := rows.Scan(&d.Day, &d.Views); err != nil {
				return nil, err
			}
			out = append(out, d)
		}
		return out, rows.Err()
	}
}

func RenderSparkline(days []DayCount, width, height int) string {
	var max int64 = 1
	for _, d := range days {
		if d.Views > max {
			max = d.Views
		}
	}
	points := make([]string, 0, len(days))
	for i, d := range days {
		x := 0
		if len(days) > 1 {
			x = i * width / (len(days) - 1)
		}
		y := height - int(d.Views*int64(height-2)/max) - 1
		points = append(points, fmt.Sprintf("%d,%d", x, y))
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="Views over the last 30 days"><polyline fill="none" stroke="#5b3cf0" stroke-width="2" points="%s"/></svg>`,
		width, height, width, height, strings.Join(points, " "))
}

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Key}} views</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:40rem;margin:3rem auto;padding:0 16px;color:#1c1a2b;background:#fbfbfd}
@media (prefers-color-scheme:dark){body{color:#f4f3f8;background:#151320}}h1{font-size:1.25rem;word-break:break-all}.total{font-size:2.5rem;font-weight:700}small{opacity:.7}</style>
</head><body><h1>{{.Key}}</h1><p class="total">{{.Total}}</p><p><small>views, not unique visitors. Last 30 days:</small></p>
{{.Sparkline}}
<p><a href="{{.Homepage}}/?ref=counter">Track your own site with Supermetry</a></p></body></html>`))

func (h *Handler) servePage(w http.ResponseWriter, r *http.Request, key string) {
	if h.Stats == nil || h.Blocklist[key] {
		http.NotFound(w, r)
		return
	}
	today := h.Now().UTC()
	from, to := today.AddDate(0, 0, -29).Format(time.DateOnly), today.Format(time.DateOnly)
	days, err := h.Stats(r.Context(), key, from, to)
	if err != nil {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:")
	_ = pageTmpl.Execute(w, map[string]any{
		"Key": key, "Total": pixels.FormatCount(h.Recorder.Total(key)),
		"Sparkline": template.HTML(RenderSparkline(days, 300, 40)), // built from integers only
		"Homepage":  strings.TrimRight(h.HomepageURL, "/"),
	})
}
```

The page reads the DB (stats page, not the counting hot path), cached for 5 minutes by the CDN. Remove the Task 11 stub `StatsFunc`.

- [ ] **Step 4: Implement `cleanup.go`**

```go
package opencounters

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Cleanup removes counters idle for 365 days and daily rows older than 400.
// These tables are small and unpartitioned, so DELETE is used (documented
// exception to the drop-partition rule).
func Cleanup(ctx context.Context, pool *pgxpool.Pool, today time.Time) (int64, error) {
	day := today.UTC().Format(time.DateOnly)
	tag, err := pool.Exec(ctx, `delete from open_counters where last_seen_day < $1::date - 365`, day)
	if err != nil {
		return 0, fmt.Errorf("delete idle open counters: %w", err)
	}
	if _, err := pool.Exec(ctx, `delete from open_counter_views_daily where day < $1::date - 400`, day); err != nil {
		return 0, fmt.Errorf("delete old open counter views: %w", err)
	}
	return tag.RowsAffected(), nil
}
```

- [ ] **Step 5: Run and commit page + cleanup**

Run: `go vet ./internal/opencounters && go test ./internal/opencounters -count=1`
Expected: `ok`.

```bash
git add internal/opencounters/page.go internal/opencounters/page_test.go
git commit -m "feat(opencounters): add public counter stats page"
git add internal/opencounters/cleanup.go internal/opencounters/cleanup_test.go
git commit -m "feat(opencounters): clean up idle counters and old rows"
```

- [ ] **Step 6: Wire into the server**

`internal/api/router.go` deps: `OpenCounters *opencounters.Handler` and `OpenCountersEnabled bool`; in routes, after the pixel block: `if deps.OpenCounters != nil { opencounters.Register(mux, deps.OpenCounters, deps.OpenCountersEnabled) } else { opencounters.Register(mux, nil, false) }`.

`cmd/supermetry/serve.go`:

```go
	var counterHandler *opencounters.Handler
	if cfg.OpenCounters {
		blocklist, err := opencounters.LoadBlocklist(cfg.OpenCounterBlocklist)
		if err != nil {
			return fmt.Errorf("load open counter blocklist: %w", err)
		}
		counterRecorder := opencounters.NewRecorder(opencounters.PoolStore(pool), 50, 500)
		counterHandler = &opencounters.Handler{
			Recorder: counterRecorder, Proxy: pixels.NewProxyLimiter(), Blocklist: blocklist,
			Branded: cfg.BadgeBranding, HomepageURL: cfg.HomepageURL, Now: settings.clock.Now,
			Stats: opencounters.PoolStats(pool),
		}
		// start counterRecorder.Run alongside linkRecorder.Run / pixelRecorder.Run
	}
```

Pass `OpenCounters: counterHandler, OpenCountersEnabled: cfg.OpenCounters` into router deps. Add the job (only when enabled):

```go
		{Name: "open-counters-cleanup", Every: 24 * time.Hour, Run: func(ctx context.Context) error {
			if !cfg.OpenCounters {
				return nil
			}
			_, err := opencounters.Cleanup(ctx, pool, clk.Now())
			return err
		}},
```

Add a router test in `internal/api` (style of the existing router tests: `grep -n "func Test" internal/api/router_test.go | head`) asserting `GET /c/github.com/a.svg` is 404 with the default config.

- [ ] **Step 7: Verify and commit wiring + docs**

Run: `go build ./... && go vet ./... && go test ./internal/api ./cmd/supermetry ./internal/opencounters -count=1 && make api-inventory`
Expected: all `ok`; document `/c/{rest}` (public, off by default) in `openapi.yaml` so the inventory passes.

```bash
git add internal/api/router.go internal/api/router_test.go cmd/supermetry/serve.go
git commit -m "feat(server): mount open counters behind SUPERMETRY_OPEN_COUNTERS"
git add internal/api/openapi.yaml
git commit -m "docs(api): document open counter endpoints"
```

### Task 13: Web onboarding asks "What do you want to measure?"

**Files:**
- Create: `web/src/features/sites/KindChoice.tsx`, `web/src/features/sites/PixelProjectForm.tsx`, `web/src/features/sites/KindChoice.test.tsx`
- Modify: `web/src/features/sites/Onboarding.tsx` (choice before the form; pixel projects land on the dashboard, not installation)
- Modify: `web/src/features/sites/types.ts` (`SiteInput`, `ManagedSite.kind`), `web/src/lib/me.ts:8` (`Site.kind?: 'web' | 'pixel'`)
- Modify: `web/src/messages/en.ts` (new `pixels` block)

**Interfaces:**
- Consumes: `createSite(input, orgId)` (`web/src/features/sites/api.ts:42`); `siteResponse.kind` (Task 2; `/api/private/me` reuses `siteResponse`, so memberships carry `kind`).
- Produces: `type SiteKind = 'web' | 'pixel'`; `type SiteInput = WebSiteInput | PixelSiteInput` where `PixelSiteInput = { kind: 'pixel'; name: string; timezone: string; retention_days: RetentionDays }`; `<KindChoice value onChange />`; `<PixelProjectForm submitLabel onSubmit />`; `en.pixels.*` strings.

- [ ] **Step 1: Write the failing test** (`KindChoice.test.tsx`)

```tsx
import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { Onboarding } from './Onboarding'
import type { Me } from '@/routes/app.$org'

afterEach(cleanup)

const me = {
  demo_prompt_dismissed: true,
  memberships: [{ org: { id: 1, slug: 'acme', name: 'Acme' }, role: 'owner', sites: [], site_ids: [], capabilities: [], effective_capabilities: [] }],
} as unknown as Me

describe('Onboarding kind choice', () => {
  it('creates a pixel project with only name and time zone', async () => {
    const createSite = vi.fn().mockResolvedValue({ id: 9, slug: 'readme', public_id: 'abc', kind: 'pixel' })
    const navigate = vi.fn()
    render(<Onboarding me={me} createSite={createSite} navigate={navigate} />)
    expect(screen.getByRole('heading', { name: 'What do you want to measure?' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('radio', { name: /Views anywhere/ }))
    expect(screen.queryByLabelText('Domains')).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Project name'), { target: { value: 'README views' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create project' }))
    await waitFor(() => expect(createSite).toHaveBeenCalledWith(expect.objectContaining({ kind: 'pixel', name: 'README views', retention_days: 365 })))
    expect(createSite.mock.calls[0][0]).not.toHaveProperty('hosts')
    expect(navigate).toHaveBeenCalledWith('/app/acme/readme')
  })

  it('keeps the website form as the default', () => {
    render(<Onboarding me={me} createSite={vi.fn()} navigate={vi.fn()} />)
    expect(screen.getByRole('radio', { name: /Website/ })).toBeChecked()
    expect(screen.getByLabelText('Domains')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && bun run test --run src/features/sites/KindChoice.test.tsx`
Expected: FAIL, `Unable to find role "heading" with name "What do you want to measure?"`.

- [ ] **Step 3: Implement**

`en.ts` (inside the exported object):

```ts
  pixels: {
    measureTitle: 'What do you want to measure?',
    website: 'Website',
    websiteHint: 'Web analytics with a script on your site.',
    anywhere: 'Views anywhere',
    anywhereHint: 'Pixel analytics for GitHub, READMEs and docs.',
    projectName: 'Project name',
    createProject: 'Create project',
    viewsNote: 'views, not unique visitors',
    noCountryNote: 'GitHub and other image proxies hide the country, so their views have none.',
  },
```

`types.ts`:

```ts
export type SiteKind = 'web' | 'pixel'
export type WebSiteInput = { kind?: 'web'; name: string; hosts: string[]; timezone: string; retention_days: RetentionDays; revenue_currency: string }
export type PixelSiteInput = { kind: 'pixel'; name: string; timezone: string; retention_days: RetentionDays }
export type SiteInput = WebSiteInput | PixelSiteInput
```

and `kind: SiteKind` on `ManagedSite`. Fix any `input.hosts` use in `SiteForm.tsx` by typing its `onSubmit` as `(input: WebSiteInput) => Promise<void>`.

`KindChoice.tsx` (shadcn `RadioGroup`; add it with `bunx shadcn@latest add radio-group` if `src/components/ui/radio-group.tsx` is missing):

```tsx
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import type { SiteKind } from '@/features/sites/types'
import { en } from '@/messages/en'

export function KindChoice({ value, onChange }: { value: SiteKind; onChange: (kind: SiteKind) => void }) {
  const options = [
    { kind: 'web' as const, title: en.pixels.website, hint: en.pixels.websiteHint },
    { kind: 'pixel' as const, title: en.pixels.anywhere, hint: en.pixels.anywhereHint },
  ]
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="sr-only">{en.pixels.measureTitle}</legend>
      <RadioGroup value={value} onValueChange={(next) => onChange(next as SiteKind)} className="grid gap-2 sm:grid-cols-2">
        {options.map((option) => (
          <Label key={option.kind} htmlFor={`kind-${option.kind}`} className="flex cursor-pointer items-start gap-3 rounded-lg border p-3 has-[[data-state=checked]]:border-primary">
            <RadioGroupItem id={`kind-${option.kind}`} value={option.kind} aria-label={`${option.title}: ${option.hint}`} />
            <span className="flex flex-col gap-0.5">
              <span className="font-medium">{option.title}</span>
              <span className="text-xs text-muted-foreground">{option.hint}</span>
            </span>
          </Label>
        ))}
      </RadioGroup>
    </fieldset>
  )
}
```

`PixelProjectForm.tsx`: a form with `Project name` input and the same time-zone select `SiteForm` uses (import its time-zone field component, or copy the `<select>` it renders if it is inline), submitting `{ kind: 'pixel', name, timezone, retention_days: 365 }`, button label from props.

```tsx
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { TimezoneSelect } from '@/features/sites/SiteForm'
import type { PixelSiteInput } from '@/features/sites/types'
import { en } from '@/messages/en'

export function PixelProjectForm({ submitLabel, onSubmit }: { submitLabel: string; onSubmit: (input: PixelSiteInput) => Promise<void> }) {
  const [name, setName] = useState('')
  const [timezone, setTimezone] = useState(() => Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={async (event) => {
        event.preventDefault()
        setBusy(true)
        setError(null)
        try {
          await onSubmit({ kind: 'pixel', name: name.trim(), timezone, retention_days: 365 })
        } catch (err) {
          setError(err instanceof Error ? err.message : String(err))
        } finally {
          setBusy(false)
        }
      }}
    >
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="pixel-project-name">{en.pixels.projectName}</Label>
        <Input id="pixel-project-name" required maxLength={120} value={name} onChange={(e) => setName(e.target.value)} />
      </div>
      <TimezoneSelect value={timezone} onChange={setTimezone} />
      {error ? <p role="alert" className="text-sm text-destructive">{error}</p> : null}
      <Button type="submit" disabled={busy || name.trim() === ''}>{submitLabel}</Button>
    </form>
  )
}
```

If `SiteForm.tsx` has no exported `TimezoneSelect`, extract its time-zone field into an exported `TimezoneSelect({ value, onChange })` in the same file first (no behaviour change; `sites.test.tsx` must still pass).

`Onboarding.tsx`: add `const [kind, setKind] = useState<SiteKind>('web')`; change the header to `<PageHeader title={en.pixels.measureTitle} />`, render `<KindChoice value={kind} onChange={setKind} />`, then either the existing `<SiteForm …/>` or:

```tsx
<PixelProjectForm
  submitLabel={en.pixels.createProject}
  onSubmit={async (input) => {
    const site = await (createSite ?? ((value) => createManagedSite(value, membership.org.id)))(input)
    navigate(`/app/${membership.org.slug}/${site.slug ?? site.public_id}`)
  }}
/>
```

Apply the same choice to the "Add site" dialog in `SitesPage.tsx` if it renders `SiteForm` directly (`grep -n "<SiteForm" web/src`).

- [ ] **Step 4: Run web checks**

Run: `cd web && bun run test --run src/features/sites && bun run typecheck`
Expected: all tests pass (including the existing `sites.test.tsx`, whose "Create your first site" heading assertion must be updated to the new title), typecheck exit 0.

- [ ] **Step 5: Commit (two commits)**

```bash
git add web/src/features/sites/types.ts web/src/lib/me.ts web/src/messages/en.ts web/src/features/sites/SiteForm.tsx
git commit -m "feat(web): add project kind types and copy"
git add web/src/features/sites/KindChoice.tsx web/src/features/sites/PixelProjectForm.tsx web/src/features/sites/KindChoice.test.tsx web/src/features/sites/Onboarding.tsx web/src/features/sites/SitesPage.tsx web/src/features/sites/sites.test.tsx web/src/components/ui/radio-group.tsx
git commit -m "feat(web): choose website or views anywhere when creating a project"
```

### Task 14: Pixel dashboard, snippets and sidebar

**Files:**
- Create: `web/src/features/pixels/api.ts`, `web/src/features/pixels/snippets.ts`, `web/src/features/pixels/snippets.test.ts`
- Create: `web/src/features/pixels/PixelDashboard.tsx`, `web/src/features/pixels/PixelList.tsx`, `web/src/features/pixels/PixelDashboard.test.tsx`
- Modify: `web/src/routes/app.$org.$site.index.tsx:~78` (branch on `site.kind === 'pixel'`)
- Modify: `web/src/components/Sidebar.tsx:96-100` (`visibleNavigation` hides web-only items for pixel projects), `web/src/components/Sidebar.test.tsx`
- Modify: `internal/platform/http/handlers_sites.go` / `siteResponse` — add `homepage_url`? No: the homepage comes from the existing public config endpoint; check `grep -rn "homepage_url" web/src internal/platform/http | head`. If it is not exposed, add `HomepageURL string \`json:"homepage_url"\`` to the `/api/private/me` response (Task 2 style, with OpenAPI) in a separate commit before the UI commit.

**Interfaces:**
- Consumes: Task 8 endpoints and generated types `components['schemas']['Pixel']`, `['PixelStats']`.
- Produces:
  - `snippets.ts`: `export function pixelSnippets(p: { svg_url: string; gif_url: string }, homepage: string): { linkedMarkdown: string; markdown: string; invisibleMarkdown: string; html: string }`
  - `api.ts`: `usePixels(siteId)`, `usePixelStats(siteId, { from, to, pixel? })`, `useCreatePixel(siteId)`, `useUpdatePixel(siteId)`, `useDeletePixel(siteId)` (TanStack Query, keys `['pixels', siteId]`, `['pixel-stats', siteId, from, to, pixel]`)
  - `<PixelDashboard site />`
  - `PIXEL_NAV_SUFFIXES = ['', '/sites', '/health']` exported from `Sidebar.tsx`

- [ ] **Step 1: Write the failing snippet test**

```ts
import { describe, expect, it } from 'vitest'

import { pixelSnippets } from './snippets'

describe('pixelSnippets', () => {
  const p = { svg_url: 'https://sm.example/px/pAAAAAAAAAAA.svg', gif_url: 'https://sm.example/px/pAAAAAAAAAAA.gif' }
  it('links the badge to the homepage by default and offers unlinked variants', () => {
    const s = pixelSnippets(p, 'https://supermetry.com/')
    expect(s.linkedMarkdown).toBe('[![views](https://sm.example/px/pAAAAAAAAAAA.svg)](https://supermetry.com/?ref=badge)')
    expect(s.markdown).toBe('![views](https://sm.example/px/pAAAAAAAAAAA.svg)')
    expect(s.invisibleMarkdown).toBe('![](https://sm.example/px/pAAAAAAAAAAA.gif)')
    expect(s.html).toBe('<img src="https://sm.example/px/pAAAAAAAAAAA.gif" alt="" width="1" height="1" referrerpolicy="no-referrer-when-downgrade">')
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && bun run test --run src/features/pixels/snippets.test.ts`
Expected: FAIL, `Failed to resolve import "./snippets"`.

- [ ] **Step 3: Implement `snippets.ts`**

```ts
export function pixelSnippets(p: { svg_url: string; gif_url: string }, homepage: string) {
  const home = homepage.replace(/\/+$/, '')
  return {
    linkedMarkdown: `[![views](${p.svg_url})](${home}/?ref=badge)`,
    markdown: `![views](${p.svg_url})`,
    invisibleMarkdown: `![](${p.gif_url})`,
    html: `<img src="${p.gif_url}" alt="" width="1" height="1" referrerpolicy="no-referrer-when-downgrade">`,
  }
}
```

Run the test again; expected PASS. Commit:

```bash
git add web/src/features/pixels/snippets.ts web/src/features/pixels/snippets.test.ts
git commit -m "feat(web): build pixel and badge snippets"
```

- [ ] **Step 4: Write the failing dashboard + sidebar tests**

`PixelDashboard.test.tsx` (mock `fetch` the way neighbouring dashboard tests do; `grep -rn "vi.stubGlobal('fetch'" web/src | head -3` and reuse that helper):

```tsx
import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { PixelDashboard } from './PixelDashboard'

afterEach(() => { cleanup(); vi.unstubAllGlobals() })

const pixel = { id: 1, public_id: 'pAAAAAAAAAAA', name: 'GitHub profile', badge_label: 'views', enabled: true, created_at: '2026-10-01T00:00:00Z', total: 1234, views_30d: 56, gif_url: 'https://sm.example/px/pAAAAAAAAAAA.gif', svg_url: 'https://sm.example/px/pAAAAAAAAAAA.svg' }
const stats = { from: '2026-09-15', to: '2026-10-14', daily: [{ day: '2026-10-14', proxy: 50, direct: 6 }], pixels: [{ pixel_id: 1, views: 56 }], countries: [{ country: 'NP', views: 6 }] }

describe('PixelDashboard', () => {
  it('shows views, the pixel list with snippets, and the country note', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => new Response(JSON.stringify(String(url).includes('/stats') ? stats : [pixel]), { headers: { 'Content-Type': 'application/json' } })))
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <PixelDashboard site={{ id: 7, name: 'README', kind: 'pixel', timezone: 'UTC' }} homepage="https://supermetry.com" />
      </QueryClientProvider>,
    )
    expect(await screen.findByText('GitHub profile')).toBeInTheDocument()
    expect(screen.getByText('1.2k')).toBeInTheDocument()
    expect(screen.getByText(/views, not unique visitors/)).toBeInTheDocument()
    expect(screen.getByText(/hide the country/)).toBeInTheDocument()
    expect(screen.getByRole('img', { name: /GitHub profile badge preview/ })).toHaveAttribute('src', pixel.svg_url)
    expect(screen.getByRole('button', { name: /Copy linked Markdown badge/ })).toBeInTheDocument()
  })
})
```

Add to `web/src/components/Sidebar.test.tsx`:

```ts
it('hides web-only pages for pixel projects', () => {
  const items = visibleNavigation('owner', true, undefined, { id: 1, name: 'R', kind: 'pixel' } as Site)
  expect(items.map((item) => item.suffix)).toEqual(['', '/sites', '/health'])
})
```

- [ ] **Step 5: Run to verify they fail**

Run: `cd web && bun run test --run src/features/pixels src/components/Sidebar.test.tsx`
Expected: FAIL (`./PixelDashboard` unresolved; sidebar returns web items).

- [ ] **Step 6: Implement**

`Sidebar.tsx`:

```ts
export const PIXEL_NAV_SUFFIXES: readonly string[] = ['', '/sites', '/health']
```

and at the end of `visibleNavigation`, filter: `.filter((item) => site?.kind !== 'pixel' || PIXEL_NAV_SUFFIXES.includes(item.suffix))`. Also hide `ReplayNavLink` when `site?.kind === 'pixel'` (`replaysAvailable` returns false for pixel sites).

`api.ts`:

```ts
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import type { components } from '@/lib/api.gen'
import { privateRequest } from '@/lib/privateRequest' // same import api.ts in features/sites uses

export type Pixel = components['schemas']['Pixel']
export type PixelStats = components['schemas']['PixelStats']
export type PixelInput = components['schemas']['PixelInput']

export const usePixels = (siteId: number) =>
  useQuery({ queryKey: ['pixels', siteId], queryFn: () => privateRequest<Pixel[]>('GET', `/api/private/sites/${siteId}/pixels`) })

export const usePixelStats = (siteId: number, range: { from: string; to: string; pixel?: number }) =>
  useQuery({
    queryKey: ['pixel-stats', siteId, range.from, range.to, range.pixel ?? 0],
    queryFn: () => {
      const q = new URLSearchParams({ from: range.from, to: range.to })
      if (range.pixel) q.set('pixel', String(range.pixel))
      return privateRequest<PixelStats>('GET', `/api/private/sites/${siteId}/pixels/stats?${q}`)
    },
  })

function useInvalidate(siteId: number) {
  const client = useQueryClient()
  return () => Promise.all([client.invalidateQueries({ queryKey: ['pixels', siteId] }), client.invalidateQueries({ queryKey: ['pixel-stats', siteId] })])
}

export function useCreatePixel(siteId: number) {
  const invalidate = useInvalidate(siteId)
  return useMutation({ mutationFn: (body: PixelInput) => privateRequest<Pixel>('POST', `/api/private/sites/${siteId}/pixels`, { body }), onSuccess: invalidate })
}

export function useUpdatePixel(siteId: number) {
  const invalidate = useInvalidate(siteId)
  return useMutation({ mutationFn: ({ id, ...body }: PixelInput & { id: number }) => privateRequest<Pixel>('PATCH', `/api/private/sites/${siteId}/pixels/${id}`, { body }), onSuccess: invalidate })
}

export function useDeletePixel(siteId: number) {
  const invalidate = useInvalidate(siteId)
  return useMutation({ mutationFn: (id: number) => privateRequest<void>('DELETE', `/api/private/sites/${siteId}/pixels/${id}`), onSuccess: invalidate })
}
```

(Match the real path of `privateRequest`: `grep -n "privateRequest" web/src/features/sites/api.ts | head -2`.)

`PixelDashboard.tsx` — layout: page header with a "Create pixel" button (dialog: name + badge label → `useCreatePixel`); a stacked area chart (shadcn chart, series `proxy` and `direct`, from `usePixelStats` for the last 30 days in the site zone) with a pixel `<Select>` filter ("All pixels" or one) and the caption `en.pixels.viewsNote`; `<PixelList>`; a countries card (top 10 rows from `stats.countries`) with `en.pixels.noCountryNote` beneath. Reuse the chart wrapper the links pixel page uses (`grep -n "Chart" web/src/routes/app.\$org.\$site.links.pixel.tsx`), so styling matches. Format numbers with a TS port of `FormatCount`:

```ts
export function formatViews(n: number): string {
  const units: [number, string][] = [[1e9, 'B'], [1e6, 'M'], [1e3, 'k']]
  for (let i = 0; i < units.length; i++) {
    const [div, suffix] = units[i]
    if (n < div) continue
    const tenths = Math.floor((n * 10 + div / 2) / div)
    if (tenths >= 10000 && i > 0) return `1${units[i - 1][1]}`
    return tenths % 10 === 0 ? `${tenths / 10}${suffix}` : `${Math.floor(tenths / 10)}.${tenths % 10}${suffix}`
  }
  return String(n)
}
```

`PixelList.tsx` — a table (TanStack Table not needed for ≤100 rows; a shadcn `<Table>` is enough) with columns: name, total (`formatViews(total)`), 30-day views, enabled `<Switch>` (→ `useUpdatePixel`), badge preview `<img src={svg_url} alt={`${name} badge preview`} height={20}>`, and a "Copy" dropdown whose items are `Copy linked Markdown badge`, `Copy Markdown badge`, `Copy invisible Markdown pixel`, `Copy HTML <img>` from `pixelSnippets(pixel, homepage)` via `navigator.clipboard.writeText`, plus Delete with a confirm dialog.

In `app.$org.$site.index.tsx` `DashboardStateRoute`, after resolving `site`: `if (site?.kind === 'pixel') return <PixelDashboard site={site} homepage={me.homepage_url ?? 'https://supermetry.com'} />` (place it before any web-dashboard hooks run, or split into a wrapper component so hook order stays stable).

- [ ] **Step 7: Run web checks**

Run: `cd web && bun run test --run src/features/pixels src/components/Sidebar.test.tsx && bun run typecheck`
Expected: tests pass, typecheck exit 0.

- [ ] **Step 8: Commit (three commits)**

```bash
git add web/src/features/pixels/api.ts
git commit -m "feat(web): add pixel API hooks"
git add web/src/features/pixels/PixelDashboard.tsx web/src/features/pixels/PixelList.tsx web/src/features/pixels/PixelDashboard.test.tsx 'web/src/routes/app.$org.$site.index.tsx'
git commit -m "feat(web): show the pixel project dashboard"
git add web/src/components/Sidebar.tsx web/src/components/Sidebar.test.tsx
git commit -m "feat(web): hide web-only pages for pixel projects"
```

### Task 15: End-to-end test

**Files:**
- Create: `web/e2e/pixels.spec.ts`
- Modify: `Makefile` (`test-e2e`: add `e2e/pixels.spec.ts` to the links/settings batch)

**Interfaces:**
- Consumes: the `owner` fixture from `web/e2e/fixtures.ts` (`owner.login`, `owner.orgSlug`, `owner.apiHeaders`, `owner.orgId` — check the fixture for exact names), Tasks 2, 7, 8, 13, 14.

- [ ] **Step 1: Write the test**

```ts
import { expect, test } from './fixtures'

const camoUA = 'github-camo (876de43e)'

test('pixel project: create, add a pixel, count a Camo view, see it on the dashboard', async ({ page, owner }) => {
  await owner.login(page, `/app/${owner.orgSlug}/sites`)
  const created = await page.request.post('/api/private/sites', {
    headers: owner.apiHeaders,
    data: { name: 'README views', kind: 'pixel', timezone: 'UTC', retention_days: 365 },
  })
  expect(created.status(), await created.text()).toBe(201)
  const site = await created.json()
  expect(site.kind).toBe('pixel')

  const pixel = await page.request.post(`/api/private/sites/${site.id}/pixels`, { headers: owner.apiHeaders, data: { name: 'GitHub profile' } })
  expect(pixel.status(), await pixel.text()).toBe(201)
  const { public_id: publicId } = await pixel.json()

  const svg = await page.request.get(`/px/${publicId}.svg`, { headers: { 'User-Agent': camoUA } })
  expect(svg.status()).toBe(200)
  expect(svg.headers()['content-type']).toBe('image/svg+xml')
  expect(await svg.text()).toContain('<svg')

  await expect.poll(async () => {
    const list = await (await page.request.get(`/api/private/sites/${site.id}/pixels`)).json()
    return list[0]?.total as number
  }, { timeout: 20_000 }).toBe(1)

  await page.goto(`/app/${owner.orgSlug}/${site.slug}`)
  await expect(page.getByText('GitHub profile')).toBeVisible()
  await expect(page.getByText(/views, not unique visitors/)).toBeVisible()
  await expect(page.getByRole('link', { name: /Sessions/ })).toHaveCount(0)

  const unknown = await page.request.get('/px/pZZZZZZZZZZZ.gif', { headers: { 'User-Agent': camoUA } })
  expect(unknown.status()).toBe(200)
  expect(unknown.headers()['content-type']).toBe('image/gif')
})
```

The badge total updates in memory immediately, but `views_30d`/stats need the 10-second flush, hence the poll on `total` and no assertion on the chart's numbers.

- [ ] **Step 2: Run it**

Run: `make build && cd web && bunx playwright test e2e/pixels.spec.ts --reporter=list --workers=1`
Expected: `1 passed`.

- [ ] **Step 3: Commit**

```bash
git add web/e2e/pixels.spec.ts Makefile
git commit -m "test(e2e): cover pixel project creation and counting"
```

### Task 16: Docs (CHANGELOG, features.md, configuration)

**Files:**
- Modify: `CHANGELOG.md` (Unreleased → Added), `features.md` (move pixel analytics to done), the configuration reference that lists `SUPERMETRY_HOMEPAGE_URL` (`grep -rln "SUPERMETRY_HOMEPAGE_URL" docs/ README.md`)

- [ ] **Step 1: Write the entries**

CHANGELOG (under `## Unreleased` / `### Added`):

```markdown
- Pixel analytics projects ("Views anywhere"): 1×1 GIF pixels and SVG view-count badges for GitHub profiles, READMEs and docs. Views are daily aggregates split by image proxy and direct; no IPs or user agents are stored. Up to 100 pixels per project.
- Badges carry a small Supermetry mark; set `SUPERMETRY_BADGE_BRANDING=false` to remove it. `?style=flat|flat-square` and `?color=<hex>` customise them.
- Open counters (`/c/github.com/{owner}[/{repo}].svg`), off by default: enable with `SUPERMETRY_OPEN_COUNTERS=true`; optional `SUPERMETRY_OPEN_COUNTER_BLOCKLIST` file, one key per line.
```

Configuration reference rows for `SUPERMETRY_BADGE_BRANDING` (default `true`), `SUPERMETRY_OPEN_COUNTERS` (default `false`), `SUPERMETRY_OPEN_COUNTER_BLOCKLIST` (default empty). In `features.md`, move the pixel analytics entry to "Done" with a one-line summary and list "Camo IP-range verification" and "more open-counter hosts" under "Deferred".

- [ ] **Step 2: Verify and commit**

Run: `grep -n "SUPERMETRY_OPEN_COUNTERS" CHANGELOG.md features.md docs -r | head` — expected: at least the CHANGELOG and configuration reference lines. Then `go build ./... && go vet ./...` (no code change; sanity).

```bash
git add CHANGELOG.md features.md docs
git commit -m "docs: changelog and configuration for pixel analytics"
```

---

## Follow-up in supermetry-cloud and supermetry-site

Not in this repo; track as separate PRs there.

**supermetry-cloud**
- Set `SUPERMETRY_OPEN_COUNTERS=true` on the hosted deployment.
- Serve `counter.supermetry.com` behind the CDN; no caching for `.svg`/`.gif` (respect `no-store`); allow the 5-minute cache on stats pages.
- Ship and mount the blocklist file and set `SUPERMETRY_OPEN_COUNTER_BLOCKLIST`; document how to add a key and restart.
- Monitoring: alert on open-counter flush failures, new-counter cap saturation, and `/c/` request rate.

**supermetry-site**
- Privacy policy and terms sections covering open counters (no IPs, user agents or countries stored; 365-day idle deletion).
- A short "Views anywhere" docs page with the snippet variants and the `?ref=badge` landing behaviour on the homepage.

## Self-review notes

- Spec coverage: data model (T1), kind (T2), registry/cache (T3–T4), counting rules 1–4 (T5, T7), badge cache (T4, T7), API + 409 limit (T3, T8), retention (T9), UI choice/dashboard/snippets/sidebar (T13–T14), part 2 branding/style/linked snippet (T6, T14), part 3 switch/keys/storage/counting/cap/totals/cleanup/page (T10–T12), tests incl. e2e (T15).
- Resolved ambiguities: stats take `from`/`to` instead of `period`; pixel public id is 12 chars including the `p`; open-counter totals keyed by string inside the counter recorder; counter days are UTC; pixel retention mirrors partition slack (`class + 1` days); blocklist loading lives in core because the env var is core config.
