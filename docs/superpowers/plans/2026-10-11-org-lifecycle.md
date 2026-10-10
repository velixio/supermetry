# Organization and Site Lifecycle Implementation Plan

> **For agentic workers:** run each task with `PLAN=docs/superpowers/plans/2026-10-11-org-lifecycle.md SPEC=docs/superpowers/specs/2026-10-11-org-lifecycle-design.md scripts/codex-task.sh <n>`. The runner gates (gofmt, vet, go test, web gate) and commits. Implementers never run git write commands. Steps use checkbox (`- [ ]`) syntax.

**Goal:** 7-day restorable deletion for real sites and organizations, with notice and 72-hour reminder emails when SMTP is configured, immediate erase for demo projects, plus org rename and an org delete UI.

**Architecture:** Deletion stays a soft delete (`deleted_at`). A shared `internal/platform/lifecycle` package holds the grace rules. The existing `site-erasure` job only erases sites past grace (demos at once). A new `deletion-notices` scheduler job sends the emails through `mail.Sender`. Restore endpoints undo the soft delete inside the grace period. Org restore and listing go through the account scope, because a deleted org's members can't authenticate into it.

**Tech Stack:** Go 1.26, pgx/v5, PostgreSQL (forward-only migrations), `net/http`, `mail.Sender`; web: Bun, React, TS, TanStack Router/Query, shadcn/ui.

**Spec:** [docs/superpowers/specs/2026-10-11-org-lifecycle-design.md](../specs/2026-10-11-org-lifecycle-design.md)

## Global Constraints

- Migration `internal/db/migrations/0045_deletion_grace.sql`, exactly the SQL in spec section 3. Transactional and forward-only. (`0040` is main's share expiry; `0041`–`0044` belong to the demo branch, which merges first.)
- `DeletionGrace = 7 * 24 * time.Hour`; `ReminderLead = 72 * time.Hour`.
- Demo sites (`is_demo`): no grace, no restore, no emails.
- Emails only when `mail.Sender.Enabled()`. Never log email addresses, IPs or user agents; log site/org ids only.
- **Authz:** site delete and restore are owner/admin; org delete and restore are owner (org delete keeps the existing password/TOTP check); org rename is owner/admin. Viewer and editor get 403. Another org's resources get 404.
- No hard deletes of `sites` or `orgs` rows.
- All time logic takes an injected clock.
- Commit messages: conventional, with no trailers and no mention of any AI tool or assistant.

## Review Focus

1. **A site is deleted, restored, then deleted again:** the grace clock and the email state start over, and the old reminder never fires. Tested in Tasks 131 and 133.
2. **A user's only org is scheduled for deletion:** they can still sign in, see the no-org screen and restore it. Tested in Tasks 132 and 231.
3. **An org is deleted while one of its sites was already deleted individually 3 days earlier:** restoring the org doesn't bring that site back, and that site keeps its own purge date. Tested in Task 132.
4. **SMTP is turned on 5 days after a deletion:** no stale "scheduled" email is sent, only the reminder. Tested in Task 133.
5. **The erasure job runs twice in a row:** a site already erased is never erased or listed again. Tested in Task 131.

## File Map

| Path | Task | Responsibility |
|---|---|---|
| `internal/db/migrations/0045_deletion_grace.sql` | 131 | schema |
| `internal/platform/lifecycle/lifecycle.go` (+ test) | 131 | grace constants, `PurgeAt`, `Restorable`, `ReminderDue` |
| `internal/platform/store/sites.go`, `store/sites_lifecycle_test.go` | 131 | deleted-site queries, restore, erase marks |
| `cmd/supermetry/serve.go` (`eraseDeletedSites`) | 131 | grace-aware erasure |
| `internal/platform/sitesvc/sites.go` | 131 | `Restore`, `ListDeleted` |
| `internal/platform/store/orgs.go`, `internal/platform/http/private.go` (`DeleteOrg`), `internal/platform/orgs/orgs.go` | 132 | scheduled vs immediate org delete, restore, rename |
| `internal/platform/lifecycle/notices.go` (+ test) | 133 | the `deletion-notices` job |
| `internal/platform/http/handlers_lifecycle.go` (+ test), `handlers_orgs.go`, `internal/api/openapi.yaml` | 134 | HTTP routes |
| `web/src/features/sites/*`, `web/src/routes/app.$org.settings.*`, `web/src/routes/app.account*.tsx`, `web/src/routes/app.index.tsx`, `web/src/lib/api.gen.ts` | 231 | UI |

---

### Task 131: Site deletion grace, restore, grace-aware erasure

**Files:**
- Create: `internal/db/migrations/0045_deletion_grace.sql`, `internal/platform/lifecycle/lifecycle.go`, `internal/platform/lifecycle/lifecycle_test.go`, `internal/platform/store/sites_lifecycle_test.go`
- Modify: `internal/platform/store/sites.go`, `internal/platform/sitesvc/sites.go`, `cmd/supermetry/serve.go` (`eraseDeletedSites`), and the related test in `cmd/supermetry`

**Interfaces (produce):**

```go
package lifecycle
const DeletionGrace = 7 * 24 * time.Hour
const ReminderLead = 72 * time.Hour
func PurgeAt(deletedAt time.Time) time.Time                 // deletedAt + DeletionGrace
func Restorable(deletedAt time.Time, erased bool, now time.Time) bool // !erased && now.Before(PurgeAt(deletedAt))
func ReminderDue(deletedAt, now time.Time) bool             // !now.Before(PurgeAt(deletedAt).Add(-ReminderLead))
```

- `store.SitesDueForErasure(ctx, q, now time.Time) ([]int32, error)` returns sites with `deleted_at IS NOT NULL AND erased_at IS NULL AND (is_demo OR deleted_at <= now - interval '7 days')`.
- `store.MarkSiteErased(ctx, q, id int32, at time.Time) error` sets the column and `settings.erased_at`.
- `store.DeletedSites(ctx, q, orgID int64, now time.Time) ([]store.DeletedSite, error)` returns restorable, non-demo sites of a live org: `{ID, PublicID, Name, DeletedAt, PurgeAt, WithOrg bool}`.
- `store.RestoreSite(ctx, tx, orgID int64, id int32, now time.Time) error` returns `store.ErrNotRestorable` unless the site is restorable, non-demo and its org is live. It clears `deleted_at`, `deleted_with_org`, `deletion_notified_at` and `deletion_reminded_at`.
- `sitesvc.(*Sites).Restore(ctx, principal, id int32) error`: authz, member re-check under lock as `Delete` does, audit `site.restored`, registry `Invalidate()`.
- `sitesvc.(*Sites).ListDeleted(ctx, principal) ([]store.DeletedSite, error)`.
- `eraseDeletedSites(ctx, pool, now)` iterates `SitesDueForErasure`, erases each site as today, then calls `MarkSiteErased`.

- [ ] **Step 1: Write failing tests** (testpg):
  - `lifecycle`: the `PurgeAt`, `Restorable` and `ReminderDue` boundaries (exactly 7 d; one second before; 72 h before).
  - **Store:**
    - Due-for-erasure picks a demo at once, a real site only after 7 d, and never an erased site.
    - Restore works inside grace and fails after grace, after erase, for a demo, and when the org is deleted.
    - Delete, restore, then delete again resets the clock (the new `deleted_at`) and the notice columns.
  - **Service:** viewer gets 403, admin can restore, audit row `site.restored`, the registry sees the site again.
  - **Erasure job** (fake `now`): a real site deleted at T keeps its events at T+6d23h and has none at T+7d1m, with `erased_at` set. A second run makes no erase calls (count them through a hook or check the erase timestamp is unchanged).
- [ ] **Step 2: Run them and see them fail:** `go test ./internal/platform/lifecycle/ ./internal/platform/store/ ./internal/platform/sitesvc/ ./cmd/supermetry/ -count=1`
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run `go test ./...` and see it pass.**

---

### Task 132: Organization delete (scheduled or immediate), restore, rename

**Files:**
- Modify: `internal/platform/store/orgs.go`, `internal/platform/http/private.go` (`Services.DeleteOrg`), `internal/platform/orgs/orgs.go`, `internal/platform/auth/account.go` (only if the last-owner check needs `erased_at`)
- Test: `internal/platform/store/orgs_lifecycle_test.go`, `internal/platform/orgs/orgs_lifecycle_test.go`, and the existing `DeleteOrg` tests

**Interfaces (produce):**
- `Services.DeleteOrg` keeps its signature. Inside the transaction:
  - If the org has a live non-demo site, it soft-deletes the org and sets `deleted_at` on all live sites with `deleted_with_org = true`. Audit `organization.deleted` with `{scheduled: true, sites_tombstoned}`.
  - Otherwise it soft-deletes the org and its sites without `deleted_with_org` (demo sites get erased at once by Task 131's job). Audit `{scheduled: false}`.
  - In both cases, any site already deleted keeps its own `deleted_at`.
- `store.DeletedOrgsForOwner(ctx, q, userID int64, now time.Time) ([]store.DeletedOrg, error)` returns `{ID, Name, Slug, DeletedAt, PurgeAt, Sites []string}` for orgs where the user has an owner membership, `deleted_at` is within grace, `erased_at IS NULL`, and the delete was scheduled (at least one `deleted_with_org` site).
- `orgs.(*Orgs).Restore(ctx, userID int64, orgID int64, now time.Time) error`:
  - The owner check uses memberships directly, not an org-scoped principal.
  - Returns `orgs.ErrNotRestorable` outside grace, when erased, or when not scheduled.
  - Clears the org's `deleted_at` and notice columns, plus those of its `deleted_with_org` sites.
  - Audit `organization.restored` with an `authz.Principal{UserID, OrgID}`. Invalidates the sites registry.
- `orgs.(*Orgs).Rename(ctx, principal authz.Principal, name string) error`:
  - Requires owner or admin; this needs a new `authz.ActionOrgUpdate` that allows owner and admin.
  - Trims the name; it must be 1–100 runes with no control characters (`orgs.ErrInvalidName`).
  - Updates `orgs.name` and writes audit `organization.renamed` with `{from, to}`.
- **Org purge:** extend Task 131's erasure job. After its site loop, set `orgs.erased_at = now` for deleted orgs whose sites all have `erased_at` set (or that have no sites) and whose `deleted_at <= now - 7d`. The exception is immediate deletes, which have no `deleted_with_org` sites: set `erased_at` as soon as their sites are erased.

- [ ] **Step 1: Write failing tests:**
  - An org with a real site gets a scheduled delete: sites are flagged `deleted_with_org`, and the audit has `scheduled: true`.
  - An org with only a demo, or with no sites, is deleted immediately, and `erased_at` is set after the erasure job runs.
  - **Review focus 3:** a site deleted 3 days earlier is not restored with the org, and keeps its own `deleted_at`.
  - Restore:
    - by an owner inside grace brings back the org and its `deleted_with_org` sites;
    - fails for an admin (403-equivalent error), after grace, and after erase;
    - after a restore, the user's `MembershipsFor` includes the org again.
  - Rename: validation (empty, 101 runes, control characters), viewer forbidden, admin allowed, audit row.
  - **Review focus 2:** the user's only org is scheduled for deletion. `MembershipsFor` is empty, but `DeletedOrgsForOwner` lists it.
- [ ] **Step 2: Run them and see them fail.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run `go test ./...` and see it pass.**

---

### Task 133: Deletion notice and reminder emails

**Runs in parallel with Task 132** on branch `org-lifecycle-mail`, based on Task 131's commit. Task 132's org functions are **not** available, so this task reads org deletion state straight from the `orgs` columns that migration 0045 added (`deleted_at`, `deletion_notified_at`, `deletion_reminded_at`, `erased_at`) and from `sites.deleted_with_org`. Put **all** new SQL in the new file `internal/platform/store/notices.go`, and do not edit `store/orgs.go` or `store/sites.go`, so the later merge with Task 132 is clean.

**Files:**
- Create: `internal/platform/mail/notice.go`, `internal/platform/mail/notice_test.go`, `internal/platform/lifecycle/notices.go`, `internal/platform/lifecycle/notices_test.go`, `internal/platform/store/notices.go`, `internal/platform/store/notices_test.go`
- Modify: `cmd/supermetry/serve.go` (register the job)

**Reusable email building (user, 2026-10-11).** Sending already has a shared interface: `mail.Sender` / `mail.Message`, with `Enabled()`. Reuse it. Do not add another sender. What is missing is a shared way to build a consistent message and to send one message to many recipients. Add it to the `mail` package and use it for every email in this task:

```go
// Notice is a generic transactional notice rendered as text and HTML with one layout.
type Notice struct {
	Subject     string
	Heading     string
	Paragraphs  []string   // plain text; HTML-escaped when rendered
	Items       []string   // optional bullet list (e.g. site names)
	ActionLabel string     // optional call-to-action
	ActionURL   string
	Footer      string     // optional small print
}
func (n Notice) Message(to string) Message   // Text: heading, paragraphs, "- item" lines, "Label: URL", footer; HTML: same content, escaped, minimal inline styles
// SendAll sends msg (with To replaced per recipient) to each recipient, keeps going on errors,
// and returns errors.Join of the failures (nil if all succeeded). It does nothing and returns nil
// when !s.Enabled(); callers check Enabled() themselves when they must not mark state.
func SendAll(ctx context.Context, s Sender, recipients []string, n Notice) error
```

Tests for `notice.go`:
- the text layout, golden string;
- HTML escaping of `<script>` in the heading, paragraph and item;
- no action when `ActionURL` is empty;
- `SendAll` sends to all recipients, continues after one failure and returns the joined error, and does nothing when disabled (use `mail.Fake`, adding a failure hook to `Fake` if needed).

Do **not** migrate the existing invites, reset, account, report or alert emails in this task. They can adopt `Notice` later.

**Interfaces (produce):**

```go
type Notices struct { /* pool, mail.Sender, baseURL string, now func() time.Time */ }
func NewNotices(pool *pgxpool.Pool, sender mail.Sender, baseURL string, now func() time.Time) *Notices
func (n *Notices) Run(ctx context.Context) error   // one pass
func (n *Notices) Job() scheduler.Job               // {Name: "deletion-notices", Every: 5 * time.Minute, Run: n.Run}
```

**Rules:** spec section 4.4, exactly.
- **Recipients:**
  - For sites: active (not deleted) users with an owner or admin membership in the site's org.
  - For orgs: owners.
- **Send order:** for each target, send to every recipient. Set the column only if all sends succeeded; on any error, log `{site_id|org_id, err}` and move on.
- **When the reminder window has already started and `*_notified_at` is null:** set `*_notified_at` without sending, then send the reminder.
- **Body:** plain text. Use the site/org name, the UTC purge date and time (`2006-01-02 15:04 UTC`), and the restore link.

- [ ] **Step 1: Write failing tests**, using `mail.Fake` and a fake clock:
  - The scheduled notice is sent once, to owners and admins, for a site deleted at T; no reminder yet.
  - At T+4d, the reminder is sent once.
  - At T+5d, nothing more is sent.
  - Nothing is sent for:
    - a demo;
    - a `deleted_with_org` site;
    - a site restored before T+4d (and nothing after it either).
  - **Review focus 1:** delete, restore, delete again sends a fresh notice, then a reminder relative to the new `deleted_at`.
  - Org: the notice goes to owners only and lists its sites; the reminder follows.
  - Sender failing once means nothing is marked, and the next run sends.
  - `Enabled()` false sends nothing and marks nothing.
  - **Review focus 4:** SMTP enabled at T+5d sends only the reminder.
- [ ] **Step 2: Run them and see them fail.**
- [ ] **Step 3: Implement it and register the job in `serve.go`.**
- [ ] **Step 4: Run `go test ./...` and see it pass.**

---

### Task 134: Lifecycle HTTP API

**Files:**
- Create: `internal/platform/http/handlers_lifecycle.go`, `internal/platform/http/handlers_lifecycle_test.go`
- Modify: `internal/platform/http/private.go` (routes and deps), `handlers_orgs.go`, `handlers_account.go`, `internal/api/openapi.yaml`

**Routes** (all private, session-authenticated):
- `GET /api/private/orgs/{org}/sites/deleted` returns 200 `[{id, name, deleted_at, purge_at}]`. Owner/admin.
- `POST /api/private/sites/{site}/restore` returns 204. 409 `{"error":"not_restorable"}`. Owner/admin. Look the site up **including deleted ones**, through a store query scoped to the principal's org (the registry has no deleted sites).
- `PATCH /api/private/orgs/{org}` with body `{name}` returns 200 with the org JSON. 400 `invalid_name`. Owner/admin.
- `GET /api/private/me/orgs/deleted` returns 200 `[{id, name, slug, deleted_at, purge_at, sites: [name]}]`. Account scope.
- `POST /api/private/me/orgs/{org}/restore` returns 204. 409 `not_restorable`; 404 when the user isn't an owner. Account scope.
- The existing `DELETE /api/private/orgs/{org}` responds 200 `{"scheduled": bool, "purge_at": RFC3339|null}`, not 204. Check and update the web caller in Task 231.
- **Site delete:** the existing `DELETE /api/private/sites/{site}` responds 200 `{"scheduled": bool, "purge_at": ...}`, with `scheduled=false` for demos.

- [ ] **Step 1: Write failing handler tests**, following the existing helpers:
  - each route's success path;
  - viewer 403;
  - another org 404;
  - `not_restorable` 409 cases;
  - restoring a deleted org through the account scope, for a user whose only org it was;
  - rename validation 400;
  - the delete responses carry `scheduled` and `purge_at`.
- [ ] **Step 2: Run them and see them fail.**
- [ ] **Step 3: Implement the routes and the OpenAPI changes.**
- [ ] **Step 4: Run `go test ./...` and see it pass.**

---

### Task 231: Lifecycle UI

**Runs in parallel with Tasks 132–134** (user, 2026-10-11) on branch `org-lifecycle-ui`, based on Task 131's commit. The backend routes don't exist in this tree yet.
- Do **not** edit `internal/api/openapi.yaml` or `web/src/lib/api.gen.ts`, and do not run `bun run gen:api`.
- Write a small typed client in `web/src/features/lifecycle/api.ts` that follows the Task 134 contract exactly (paths, methods, bodies, status codes, JSON fields). Use the same fetch helper the other `features/*/api.ts` files use.
- Mock it in the tests.
- Mark the file with one comment: `// Hand-written until the lifecycle routes are in openapi.yaml; then switch to the generated client.`
- The site delete call already exists in `features/sites/api.ts`. Make it accept the 200 `{scheduled, purge_at}` body, and treat an empty 204 as `{scheduled: false, purge_at: null}`.
- Do not add or change Go code.

**Files:**
- Create: `web/src/features/lifecycle/api.ts`, `DeletedSitesSection.tsx`, `DeleteOrgDialog.tsx`, `OrgNameForm.tsx`, `DeletedOrgsList.tsx`, `NoOrgScreen.tsx`, and a test file for each
- Modify: `web/src/features/sites/SitesPage.tsx` (dialog copy, mount `DeletedSitesSection`), the org settings general route under `web/src/routes/app.$org.settings*.tsx` (name form and danger zone), the account route `web/src/routes/app.account*.tsx` (deleted orgs list), `web/src/routes/app.index.tsx` (no-org screen), `web/src/components/Sidebar.tsx` (query invalidation after rename), `web/src/lib/api.gen.ts` (`bun run gen:api`)

**Behavior:** spec section 5.
- **Delete site dialog:** shows the real-site or demo copy depending on `is_demo`. After delete, the Sites page shows the section with "Deletes on {local date}" and Restore. Restore success: toast "Site restored", then invalidate the sites queries.
- **Org settings:**
  - Name form, owner/admin only; read-only text for others.
  - Danger zone, owner only, with Delete organization. The dialog reuses the existing password/TOTP sensitive-action component (find where account deletion or TOTP prompts are implemented and reuse that). Its copy depends on whether the org has non-demo sites.
  - On a scheduled delete, navigate to `/app/account` and show "Organization scheduled for deletion on {date}". On an immediate delete, navigate to `/app`.
- **Account page:** "Organizations scheduled for deletion" with Restore. After a restore, invalidate `me` and navigate to the org.
- **`/app` with no memberships:** `NoOrgScreen`, with Create organization (link to the existing new-org route) and `DeletedOrgsList`.

- [ ] **Step 1: Write failing component tests:**
  - the site dialog copy, both variants;
  - the deleted-sites section renders and restore calls the API;
  - the rename form: validation, save, read-only for a viewer;
  - delete org: both copies, navigation per response;
  - the account list with restore;
  - the no-org screen.
- [ ] **Step 2: Run them and see them fail:** `cd web && bun run test --run src/features/lifecycle`
- [ ] **Step 3: Implement** with existing shadcn components. Use sentence case, and no exclamation marks.
- [ ] **Step 4: Run `cd web && bun run typecheck && bun run test --run && bun run build` and see it pass.**

---

### Task 135: Review fixes

Findings from the branch review (2026-10-11). Each fix is test-first. **An earlier attempt was interrupted:** its partial changes are in the tree. Inspect them, keep what is correct, and finish every item. Migration `0046_deletion_fixes.sql` (forward-only, transactional) holds all the schema changes below.

**Files:** `internal/db/migrations/0046_deletion_fixes.sql`, `internal/platform/store/notices.go`, `internal/platform/store/sites.go`, `internal/platform/store/orgs.go`, `internal/platform/lifecycle/notices.go`, `internal/platform/mail/notice.go`, `internal/platform/http/private.go` (`DeleteOrg`), `internal/platform/orgs/orgs.go`, `internal/platform/sitesvc/sites.go`, `cmd/supermetry/serve.go` (`eraseDeletedSites`), `web/src/features/lifecycle/DeletedSitesSection.tsx`, `DeletedOrgsList.tsx` and `OrgNameForm.tsx`, plus all matching tests.

- [ ] **1. No site emails for sites in a deleted org.**
  - Add `and o.deleted_at is null` to the site notice and reminder query.
  - The org notice and reminder list **every** non-erased, non-demo site of the org that will be erased with it, including individually deleted sites still in grace.
  - **Test:** a site deleted individually, then its org deleted, gets no site email; the org email lists it.
- [ ] **2. An org delete never strands real data.** `DeleteOrg` takes the scheduled path when the org has any non-demo site with `erased_at IS NULL`, live or already deleted. Only orgs whose sites are all demos or erased (or that have no sites) are deleted immediately.
  - **Test:** an org whose only real site was individually deleted 2 days ago gets a scheduled delete (`scheduled: true`), and the email lists that site.
- [ ] **3. Per-recipient delivery and redacted logs.**
  - New table `deletion_notice_sends (target_type text CHECK (target_type IN ('site','org')), target_id bigint, kind text CHECK (kind IN ('scheduled','reminder')), deleted_at timestamptz, user_id bigint, sent_at timestamptz NOT NULL, PRIMARY KEY (target_type, target_id, kind, deleted_at, user_id))`.
  - Each run sends only to recipients without a row for the current `deleted_at`, inserts a row per successful send, and sets `*_notified_at`/`*_reminded_at` once every current recipient has a row.
  - `mail.SendAll` gains a per-recipient result: `SendEach(ctx, s, recipients, n) []error`, aligned with `recipients`. Keep `SendAll` as a wrapper.
  - Log failures as `{target, id, failed_count}` with **no** error text, since SMTP errors can contain addresses.
  - **Tests:**
    - one bad recipient out of three: the next run sends only to the bad one, the other two get exactly one email each, and the column is set once the bad one succeeds;
    - the log output contains no `@`.
- [ ] **4. Restore respects the site limit.** Site restore and org restore check the org's live non-demo site count against the configured `MaxSitesPerOrg` (the same rule as create, where 0 means no limit). When over the limit they return `ErrSiteLimit`, which maps to HTTP 409 `{"error":"site_limit"}`.
  - **Test:** at the limit, restore gives 409 and nothing changes.
- [ ] **5. Restore and erase can't interleave.**
  - Add `sites.erasure_started_at timestamptz`.
  - **Erasure job:** for each due site, run in one statement `UPDATE sites SET erasure_started_at = coalesce(erasure_started_at, $now) WHERE id=$1 AND erased_at IS NULL AND deleted_at IS NOT NULL AND (is_demo OR deleted_at <= $now - interval '7 days') RETURNING id`. Erase only if a row is returned, then set `erased_at`. Sites with `erasure_started_at` set and `erased_at` null are resumed on the next run.
  - **Restore:** lock the site row (`FOR UPDATE`) and refuse with `ErrNotRestorable` when `erasure_started_at` is not null. Take `now` inside the transaction.
  - **Test:** a restore attempted after the claim gets `ErrNotRestorable`; a claim attempted after a restore commits returns no row; a resumed half-erased site completes.
- [ ] **6. UI errors.** The restore buttons show the server error inline:
  - `not_restorable` → "This can no longer be restored."
  - `site_limit` → "Your organization is at its site limit."
  - anything else → a generic message.

  The rename form shows `invalid_name` and other errors. The client length check counts code points (`[...name].length`), which matches the server's rune count.
  - **Tests:** each message renders on the matching error.
- [ ] **7. A post-commit registry refresh failure doesn't fail a done restore.** In `Orgs.Restore` and `sitesvc.Restore`, an `Invalidate` error after commit is logged with `slog.Warn` and the call returns success. The periodic registry refresh picks the change up.
  - **Test:** a failing invalidator still returns nil, and the site is restored.
- [ ] **8. Spec note.** Update spec section 5: `deleted_at`/`purge_at` appear only on the deleted-items lists and the delete results, not on live site or org objects. Edit the spec file only for this sentence.
- [ ] **9. Missing tests from the review:**
  - SMTP enabled 5 days after a delete sends only the reminder (add it if absent).
  - Restoring a site whose org is deleted returns `not_restorable`.

Run `go test ./...` and the web gate, and see both pass.
