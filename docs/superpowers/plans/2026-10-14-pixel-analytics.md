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
