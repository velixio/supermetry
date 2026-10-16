# Admin Management API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Put customer management in core's instance admin API behind `SUPERMETRY_ADMIN_API` (off by default), make it callable with the admin API key, and add four abilities: delete and restore an org, set a site's retention, create a user, and log in as a customer.

**Architecture:** One config flag gates a set of `/api/private/admin/*` routes, which answer 404 when it is off. A new `admin.Actor` (a user, or the admin key) replaces the bare `actorID` in the admin services. A request-context value carries "admin key" or "impersonator" into the two audit writers, so existing audit call sites don't change. Impersonation is a normal `user_sessions` row with an impersonator and a hard 30-minute expiry, plus a return cookie for the admin's own session.

**Tech Stack:** Go (`net/http`, `pgx`), PostgreSQL migrations, React + TanStack Router/Query + shadcn (`web/`, Bun).

**Spec:** `docs/superpowers/specs/2026-10-16-admin-api-gaps-design.md`

## Global Constraints

- Flag: `SUPERMETRY_ADMIN_API`, boolean, default `false`. When off, every gated route returns the same 404 problem body as an unknown route.
- Gated routes (existing): `GET /api/private/admin/users`, `PATCH /api/private/admin/users/{user}`, `GET /api/private/admin/organizations`, `GET /api/private/admin/organizations/{org}`, `GET /api/private/admin/sites`.
- Gated routes (new):
  - `POST /api/private/admin/users`
  - `POST /api/private/admin/users/{user}/impersonate`
  - `DELETE /api/private/admin/organizations/{org}`
  - `POST /api/private/admin/organizations/{org}/restore`
  - `PATCH /api/private/admin/sites/{site}`
- Always on: settings, `security/*`, `audit`, `update*`. The admin key does not open these.
- Admin key: `Authorization: Bearer <SUPERMETRY_ADMIN_API_KEY>`, only when the flag is on and the key is ≥ 32 bytes. It acts as the `admin_api_key` actor and has one shared limit of 600 requests a minute.
- The key is refused (`403 admin_key_forbidden`) for: impersonate, changing `instance_admin`, and creating a user with `password`.
- Retention values: `0, 7, 30, 90, 365`.
- Org delete reuses D62: 7-day restore, notice and reminder emails, `site-erasure` purge.
- Set-password link: 7 days. Password reset stays at 1 hour.
- Impersonation:
  - hard expiry 30 minutes; a reason of 3–200 characters is required;
  - return cookie `supermetry_admin_return`: HttpOnly, SameSite=Strict, Secure on secure requests, path `/`;
  - blocked during impersonation: password/email/2FA changes, API key or token creation, revoking other sessions, deleting the account (`403 impersonation_forbidden`);
  - invisible to the customer: not in their sessions list, and org audit shows the customer as actor;
  - only the instance audit records `support.impersonation.started` and `.ended`.
- Never log or audit a set-password link or an admin-set password.
- Commits: velixio identity, no AI trailers, email scan before each commit (CLAUDE.md). One PR per group below, never push to main.

## Review Focus

1. **The switch turned off while sessions exist.** A live impersonation session must die on its next request. Test in Task 8.
2. **An admin key that is set but shorter than 32 bytes.** Every gated route must reject the bearer as if no key were set: 401 or 403, never acting as the admin key. Test in Task 2.
3. **Two operations racing on the same org**, such as an admin delete during an owner restore, or a double delete. The second gets `409`, never a half-deleted org. Test in Task 5.
4. **Create-user with an email that differs only in case** from an existing user must answer `409`. Test in Task 6.
5. **A forced-password-change user who never changes it.** Every route other than me, password and logout answers `403 password_change_required`, including the public API with a cookie. Test in Task 7.

---

## PR 1: switch, key and actor

### Task 1: Config flag and `/me` capability

**Files:**
- Modify: `internal/config/config.go` (field `AdminAPI bool`, parsed with `parseOptionalBool(getenv("SUPERMETRY_ADMIN_API"), "SUPERMETRY_ADMIN_API", &c.AdminAPI, &errs)`, default false)
- Modify: `internal/config/config_test.go`
- Modify: `internal/platform/http/private.go` (`Deps.AdminAPI bool`), `cmd/supermetry/serve.go` (wire it)
- Modify: `internal/platform/http/handlers_account.go` (`/me` adds `"instance": {"admin_api": bool}`)
- Modify: `.env.example`, `docs/guide/configuration.md` (row and an upgrade note: the Admin Users, Organizations and Sites pages need the flag)

**Interfaces:**
- Produces: `config.Config.AdminAPI bool`; `Deps.AdminAPI bool`; `/me` JSON `instance.admin_api`.

- [ ] **Step 1: Write the failing tests.**
  - `TestLoadAdminAPIDefaultsOff`: `Load(env(baseEnv()))` gives `AdminAPI == false`, and `SUPERMETRY_ADMIN_API=true` gives `true`.
  - `TestMeReportsAdminAPI`: `/me` for an instance admin has `instance.admin_api` equal to `Deps.AdminAPI`.
- [ ] **Step 2: Run** `go test ./internal/config/ ./internal/platform/http/ -run 'AdminAPI'`. Expected: FAIL.
- [ ] **Step 3: Implement** the field, parsing, wiring and the `/me` field.
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Commit** `feat(config): add SUPERMETRY_ADMIN_API, off by default`.

### Task 2: Gate, admin-key principal and `admin.Actor`

**Files:**
- Create: `internal/platform/admin/actor.go`
- Modify: `internal/platform/admin/admin.go` (signatures)
- Modify: `internal/platform/http/handlers_admin.go` (new `managementPrincipal`; gated handlers use it)
- Modify: `internal/platform/http/private.go:243-247` (wrap the gated routes)
- Test: `internal/platform/http/handlers_admin_management_test.go`, `internal/platform/admin/admin_test.go`

**Interfaces:**
- Produces:
  - `type Actor struct { UserID int64; AdminKey bool }` in package `admin`. `func (a Actor) Valid() bool` is true when exactly one of `UserID != 0` and `AdminKey` holds.
  - `func (s *Service) UpdateUser(ctx context.Context, actor Actor, userID int64, patch UserPatch) error`. It returns `ErrAdminKeyForbidden` when `actor.AdminKey && patch.InstanceAdmin != nil`. The other `actorID` methods keep their signatures, because they are always-on and user-only.
  - `var ErrAdminKeyForbidden = errors.New("not allowed with the admin API key")`, which `adminError` maps to `403` with code `admin_key_forbidden`.
  - `func requireAdminActor(ctx, tx, actor Actor) error`: it passes for `AdminKey`, and calls `requireAdmin` for users.
  - `func (h *handler) managementPrincipal(w, r) (admin.Actor, authz.Principal, bool)`:
    - flag off: writes the router's 404 and returns false;
    - valid key bearer: returns `Actor{AdminKey: true}` and applies the shared 600-a-minute limiter `h.adminKeyLimiter`;
    - otherwise: falls back to `adminPrincipal` with `Actor{UserID: principal.UserID}`.
  - Context helpers in `internal/platform/store/audit.go`: `func WithAdminKeyActor(ctx context.Context) context.Context`. `managementPrincipal` sets it on `r = r.WithContext(...)`.

- [ ] **Step 1: Write the failing tests** (table-driven, flag on and off):
  - `TestManagementRoutes404WhenFlagOff`: each of the 5 existing gated routes returns 404 with a body equal to `GET /api/private/admin/nope`, for an instance-admin session and for the key.
  - `TestManagementRoutesAcceptAdminKey`: with the flag on and a 40-byte key, `GET /api/private/admin/users` with the bearer returns 200 and no session is needed.
  - `TestShortAdminKeyIsRejected`: with a 20-byte key configured, the bearer gets 401 or 403 and never 200.
  - `TestAlwaysOnRoutesIgnoreAdminKey`: with the flag on, `GET /api/private/admin/settings` with only the bearer returns 401 or 403.
  - `TestAdminKeyCannotChangeInstanceAdmin`: `PATCH /admin/users/{id}` with `{"instance_admin":true}` and the bearer returns `403` with code `admin_key_forbidden`, while `{"disabled":true}` returns 200.
  - `TestAdminKeyRateLimit`: the 601st request in a minute returns 429 with `Retry-After`.
- [ ] **Step 2: Run** `go test ./internal/platform/http/ -run 'Management|AdminKey|AlwaysOn'`. Expected: FAIL.
- [ ] **Step 3: Implement** `actor.go`, the `UpdateUser` signature change (update its callers), `managementPrincipal`, the limiter, and route wiring. The 404 must reuse the router's not-found writer.
- [ ] **Step 4: Run** `go test ./internal/platform/...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(admin): gate customer management behind the switch and accept the admin key`.

### Task 3: Audit columns and context actors

**Files:**
- Create: `internal/db/migrations/0230_admin_management.sql`. It holds every schema change for this plan, so later tasks don't add migrations:
  ```sql
  ALTER TABLE users ADD COLUMN password_change_required boolean NOT NULL DEFAULT false;
  ALTER TABLE user_sessions
      ADD COLUMN impersonator_user_id bigint REFERENCES users(id) ON DELETE CASCADE,
      ADD COLUMN impersonation_reason text CHECK (impersonation_reason IS NULL OR char_length(impersonation_reason) BETWEEN 3 AND 200),
      ADD CHECK ((impersonator_user_id IS NULL) = (impersonation_reason IS NULL));
  ALTER TABLE audit_log
      ADD COLUMN impersonator_user_id bigint REFERENCES users(id) ON DELETE SET NULL,
      ADD COLUMN actor_admin_key boolean NOT NULL DEFAULT false,
      ADD CHECK (NOT actor_admin_key OR (actor_user_id IS NULL AND actor_key_id IS NULL));
  ```
- Modify: `internal/platform/store/audit.go`
- Modify: org audit list query and JSON (find it with `grep -rn "from audit_log" internal/platform`); instance audit list query (`adminListAudit`)
- Test: `internal/platform/store/audit_test.go`

**Interfaces:**
- Consumes: `WithAdminKeyActor` (Task 2).
- Produces:
  - `func WithImpersonator(ctx context.Context, impersonatorID int64) context.Context`.
  - `func actorsFrom(ctx context.Context) (adminKey bool, impersonatorID int64)`, unexported.
  - `WriteAudit` and `WriteInstanceAudit` read the context. With the admin key they write `actor_user_id = NULL` and `actor_admin_key = true`. With an impersonator they write `impersonator_user_id`.
  - Instance audit JSON items gain `actor_admin_key` and `impersonator: {id, email} | null`.
  - Org audit JSON is unchanged. It never exposes the impersonator.

- [ ] **Step 1: Write the failing tests.**
  - `TestWriteAuditRecordsAdminKey`: a write with `WithAdminKeyActor` stores a row with `actor_admin_key` true and both actor ids null.
  - `TestWriteAuditRecordsImpersonator`: a write with `WithImpersonator(ctx, 7)` and `ActorUserID: 9` stores `impersonator_user_id = 7` and `actor_user_id = 9`.
  - `TestOrgAuditHidesImpersonator`: the org audit list JSON for that row has no `impersonator` key, and its actor is user 9.
  - `TestInstanceAuditShowsImpersonator`: the instance audit JSON shows `impersonator.id == 7`.
- [ ] **Step 2: Run** `go test ./internal/platform/store/ ./internal/platform/http/ -run 'Audit'`. Expected: FAIL.
- [ ] **Step 3: Implement** the migration, the context helpers, the writers and the two list queries.
- [ ] **Step 4: Run** `go test ./internal/db/... ./internal/platform/...`. Expected: PASS.
- [ ] **Step 5: Commit** in two commits, `feat(db): add admin management columns` (migration) and `feat(audit): record the admin key and impersonator`. Push the branch and open PR 1.

---

## PR 2: org delete/restore and retention

### Task 4: Set a site's retention

**Files:**
- Modify: `internal/platform/http/handlers_admin.go` (`adminUpdateSite`), `private.go` (route)
- Modify: `internal/platform/admin/admin.go` (`SetSiteRetention`)
- Test: `internal/platform/http/handlers_admin_management_test.go`

**Interfaces:**
- Produces: `func (s *Service) SetSiteRetention(ctx context.Context, actor Actor, siteID int32, days int) (old int, err error)`. It validates with the same allowed set as `sitesvc.retentionClass` (export it as `sitesvc.ValidRetentionDays(days int) bool`). It writes instance audit `instance.site.retention_changed` and org audit with the same action, both with `{old, new}`. The handler calls `h.deps.Sites.Invalidate()` afterwards.

- [ ] **Step 1: Write the failing tests.**
  - `TestAdminSetsSiteRetention`: the body `{"retention_days":90}` returns 200, and the admin sites list shows 90 for that site.
  - Table `TestAdminRetentionRejects`:
    - `{"retention_days":14}` → 422;
    - `{"retention_days":30,"name":"x"}` → 422;
    - an unknown site → 404.
  - `TestAdminRetentionAudited`: one `instance.site.retention_changed` row with `metadata.old == 30` and `metadata.new == 90`.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.** Decode with `DisallowUnknownFields`.
- [ ] **Step 4: Run** `go test ./internal/platform/...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(admin): set a site's retention`.

### Task 5: Delete and restore an org

**Files:**
- Modify: `internal/platform/http/private.go:443` (extract the org-level body of `Services.DeleteOrg` into `func (s Services) deleteOrgTx(ctx context.Context, tx pgx.Tx, orgID int64, actorUserID int64) error` (`actorUserID` is 0 for the admin key; the audit context from Task 3 supplies the key flag). The owner path keeps its own membership checks and calls it.)
- Modify: `internal/platform/orgs/orgs.go:223` (split `Restore` into the owner-check wrapper plus `func (o *Orgs) RestoreAsAdmin(ctx context.Context, orgID int64, now time.Time) error`)
- Modify: `internal/platform/admin/queries.go` (`ListOrganizations` gains `state` with values `live` (default), `deleted` and `all`; items gain `deleted_at` and `purge_at`)
- Modify: `handlers_admin.go` (`adminDeleteOrganization`, `adminRestoreOrganization`), `private.go` (routes)
- Test: the same management test file, and `internal/platform/orgs/orgs_test.go`

**Interfaces:**
- Consumes: `managementPrincipal`, `Actor` (Task 2); the audit context (Task 3).
- Produces: `DELETE /api/private/admin/organizations/{org}` with `{"confirm_slug": string}`, which returns 204. Errors:
  - 422: wrong or missing slug;
  - 404: unknown org;
  - 409: already deleted.

  `POST .../{org}/restore` returns 200 with the org. Errors are `409 not_restorable` and `409 site_limit`. `GET /admin/organizations?state=deleted` lists restorable orgs.

- [ ] **Step 1: Write the failing tests.**
  - `TestAdminDeletesOrgThenRestores`: deleting an org with a real site returns 204. Its sites stop ingesting: the registry has no live site for that public id. The admin list with `?state=deleted` shows it, with `purge_at = deleted_at + 7 days`. Restore returns 200 and the site is live again.
  - `TestAdminDeleteNeedsSlug`: a wrong slug returns 422 and the org is untouched.
  - `TestAdminDeleteTwiceConflicts`: the second delete returns 409.
  - `TestAdminDeleteDemoOnlyOrgErasesNow`: an org with only a demo site is gone and not restorable, so restore returns 409 `not_restorable`.
  - `TestOwnerDeleteUnchanged`: the existing owner delete tests still pass, so run the whole package.
- [ ] **Step 2: Run** `go test ./internal/platform/... -run 'AdminDelete|AdminRestore|DeleteOrg|Restore'`. Expected: the new tests FAIL.
- [ ] **Step 3: Implement** the extraction, the admin handlers and the list filter. Run the delete inside a transaction with `select … for update` on the org row, so concurrent operations serialize (Review Focus 3).
- [ ] **Step 4: Run** `go test ./internal/platform/... ./cmd/supermetry/`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(admin): delete and restore an organization`. Push and open PR 2.

---

## PR 3: create user and forced password change

### Task 6: Create a user

**Files:**
- Modify: `internal/platform/auth/reset.go`. Add `func (a *Auth) StartWelcome(ctx context.Context, userID int64) (link string, err error)`: it inserts into `password_resets` with `expires_at = now + 7*24h`. When mail is enabled it sends the "Set your Supermetry password" mail and returns `""`. When mail is disabled it returns the link.
- Modify: `internal/platform/admin/admin.go`. Add `func (s *Service) CreateUser(ctx context.Context, actor Actor, in NewUser) (CreatedUser, error)`.
- Create: `internal/platform/admin/create_user.go` (types and validation)
- Modify: `handlers_admin.go` (`adminCreateUser`), `private.go` (route)
- Test: `internal/platform/admin/create_user_test.go`, management handler tests

**Interfaces:**
- Produces:
  - `type NewUser struct { Email, Name, Password string; OrgID int64; OrgRole authz.Role; NewOrgName string }`.
  - `type CreatedUser struct { User UserItem; Org OrgRef; Delivery string; Link string }`, with `OrgRef{ID int64; Slug, Name string}` and `Delivery` either `"email"` or `"link"`.
  - JSON: `{ "user": …, "org": {id, slug, name}, "delivery": "email"|"link", "link"?: string }`.
- Rules:
  - The email is normalized the same way signup normalizes it; a case-insensitive duplicate returns 409.
  - `NewOrgName` creates the org with signup's slug generation; the role defaults to owner.
  - `OrgID` requires a live org and a valid role.
  - `Password` must pass `auth`'s password rules. It sets `password_change_required = true`, and it is refused for `actor.AdminKey` with `ErrAdminKeyForbidden`.
  - Without a password, an unusable random hash is stored and `StartWelcome` is called.
  - Audit: `instance.user.created` with `{method: "link"|"password"}`, and org `member.added`.

- [ ] **Step 1: Write the failing tests.**
  - `TestCreateUserWithLinkMailDisabled`: the response has `delivery == "link"` and a link containing `/reset/`. The `password_resets` row expires at about now + 7 days, and logging in with any password fails.
  - `TestCreateUserWithLinkMailEnabled`: a fake sender gets one mail to the address, and the response has no `link`.
  - `TestCreateUserNewOrgReturnsOrg`: `org.slug` is non-empty, and a second "Acme" gets a different slug.
  - `TestCreateUserDuplicateEmailCaseInsensitive`: `A@Example.com` after `a@example.com` returns 409.
  - `TestCreateUserWithPasswordFlagsChange`: `password_change_required` is true.
  - `TestAdminKeyCannotSetPassword`: the bearer with `password` returns `403 admin_key_forbidden`.
  - `TestCreateUserAuditHasNoSecret`: the audit metadata JSON contains neither the password nor the token.
- [ ] **Step 2: Run** `go test ./internal/platform/... -run 'CreateUser|AdminKeyCannotSetPassword'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./internal/platform/...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(admin): create users with a set-password link or a password`.

### Task 7: Forced password change gate

**Files:**
- Modify: `internal/platform/auth/sessions.go` (`Session` gains `PasswordChangeRequired bool`, loaded with the user)
- Modify: `internal/platform/http/private.go` (`privatePrincipal`, or the session middleware: when the flag is set, allow only `GET /api/private/me`, `POST /api/private/account/password` and `POST /api/private/auth/logout`; everything else returns `403` with code `password_change_required`)
- Modify: the account password handler (clears the flag on success)
- Modify: the 2FA setup gate ordering (password change first)
- Test: `internal/platform/http/password_change_test.go`

**Interfaces:**
- Produces: `/me` `user.password_change_required: bool`.

- [ ] **Step 1: Write the failing tests.**
  - `TestPasswordChangeRequiredAllowsOnlyThree`: login succeeds, `/me` returns 200 with the flag true, and a table of 5 other routes (sites list, dashboards, `/api/public/v1/...` with the cookie, admin, account sessions) each returns `403 password_change_required`.
  - `TestPasswordChangeClearsFlag`: after a password change, `/me` shows the flag false and the sites list returns 200.
  - `TestPasswordChangeBeforeTwoFactorSetup`: with the 2FA policy `everyone/0d`, the password change succeeds and only then does `two_factor_setup_required` take effect.
  - `TestLogoutAllowedWhileChangeRequired`: logout returns 204.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./internal/platform/...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(auth): force a password change after an admin-set password`. Push and open PR 3.

---

## PR 4: log in as a customer

### Task 8: Start, end and expire impersonation

**Files:**
- Create: `internal/platform/auth/impersonation.go`
- Modify: `internal/platform/store/sessions.go` (`UserSession` gains `ImpersonatorUserID int64` and `ImpersonationReason string`; `CreateSession` and `scanSession` handle them; `SessionsForUser` excludes rows with an impersonator; starting an impersonation never sends a new-sign-in notice)
- Modify: `internal/platform/auth/sessions.go` (`Session` gains `ImpersonatorUserID int64` and `ImpersonationExpiresAt time.Time`; the session check re-validates the impersonator)
- Modify: `handlers_admin.go` (`adminImpersonate`), a new `handlers_impersonation.go` (`endImpersonation`), `private.go` (routes, and context `store.WithImpersonator` when a session has an impersonator)
- Modify: `handlers_account.go` (`/me` field `impersonation`)
- Test: `internal/platform/auth/impersonation_test.go`, `internal/platform/http/impersonation_test.go`

**Interfaces:**
- Produces:
  - `const ImpersonationTTL = 30 * time.Minute`; `const ReturnCookieName = "supermetry_admin_return"`.
  - `func (a *Auth) StartImpersonation(ctx context.Context, adminID, targetID int64, reason string, r *http.Request) (cookieValue string, expiresAt time.Time, err error)`.
  - `func (a *Auth) EndImpersonation(ctx context.Context, cookieValue string) error`.
  - Errors: `ErrImpersonationTarget` (target is an admin, disabled, deleted, or has a reset or change flag) → 422; `ErrImpersonationReason` → 422.
  - The session check fails, and deletes the row, when any of these hold: `now ≥ created_at + ImpersonationTTL`; the impersonator is no longer an enabled instance admin; `Deps.AdminAPI` is false. The return-cookie session is checked by the handler on end.
  - `POST /api/private/admin/users/{user}/impersonate` with `{"reason": string}` returns 200 `{expires_at}`. It sets `supermetry_session` to the new session and `supermetry_admin_return` to the admin's old cookie value. It needs `Actor.UserID != 0`; the key gets `403 admin_key_forbidden`. A session that is itself impersonating gets 403.
  - `POST /api/private/auth/impersonation/end` returns 204. It deletes the impersonation session, moves the return cookie back to `supermetry_session` when that session is still valid, and clears the return cookie.
  - `/me` gains `impersonation: {by: {id, email}, started_at, expires_at, reason} | null`.
  - Instance audit: `support.impersonation.started` and `support.impersonation.ended` with `{target_user_id, reason}`. Nothing goes to the org audit.

- [ ] **Step 1: Write the failing tests** (fake clock).
  - `TestImpersonationStartsAndEnds`: `/me` as the target shows `impersonation.by.id == admin`. End restores the admin: `/me` user is the admin.
  - `TestImpersonationExpiresAfter30Minutes`: at +29m `/me` is the target; at +31m the request is unauthenticated and the row is gone.
  - Table `TestImpersonationRefusedTargets`: an instance admin, a disabled user, and a user with a reset or change flag each return 422.
  - `TestImpersonationNeedsReason`: `"ab"` returns 422.
  - `TestAdminKeyCannotImpersonate`: the bearer returns `403 admin_key_forbidden`.
  - `TestNoNestedImpersonation`: impersonating from an impersonation session returns 403.
  - `TestImpersonationEndsWhenAdminDemoted`: demote the admin, and the target's next request is unauthenticated.
  - `TestImpersonationEndsWhenSwitchOff`: rebuild the handler with `AdminAPI=false`, and the impersonation cookie no longer authenticates (Review Focus 1).
  - `TestImpersonationHiddenFromCustomer`: the target's `GET /account/sessions` (from the target's own session) does not list it.
  - `TestImpersonationAuditOnlyInstance`: instance audit has started and ended; the target's org audit has neither.
  - `TestReturnCookieAttributes`: HttpOnly, SameSite=Strict, Path `/`.
- [ ] **Step 2: Run** `go test ./internal/platform/... -run 'Impersonat|ReturnCookie'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Never extend `expires_at` past `created_at + 30m` for impersonation rows.
- [ ] **Step 4: Run** `go test ./internal/platform/...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(auth): log in as a customer for 30 minutes`.

### Task 9: Blocked actions and attribution during impersonation

**Files:**
- Modify: the account handlers for password, email, 2FA (enrol, disable, recovery codes), API keys and personal tokens, revoke-other-sessions and delete-account. Each calls `h.forbidDuringImpersonation(w, r) bool` first.
- Test: `internal/platform/http/impersonation_test.go`

**Interfaces:**
- Consumes: the `Session.ImpersonatorUserID` field (Task 8).
- Produces: `func (h *handler) forbidDuringImpersonation(w http.ResponseWriter, r *http.Request) bool`, which writes `403` with code `impersonation_forbidden`.

- [ ] **Step 1: Write the failing tests.**
  - `TestImpersonationBlocksAccountTakeover`: a table of the routes above (find the exact paths with `grep -n "account" internal/platform/http/private.go`), each returning 403 `impersonation_forbidden` from an impersonation session and its normal status from the user's own session.
  - `TestImpersonationAllowsOrgChanges`: renaming a site returns 200, and its org audit row has `actor_user_id = target` while the instance-side `impersonator_user_id = admin`.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./internal/platform/... ./cmd/supermetry/`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(auth): block account takeover actions while impersonating`. Push and open PR 4.

---

## PR 5: dashboard

### Task 10: Admin pages behind the switch, and the new actions

**Files:**
- Modify: `web/src/lib/me.ts` (types: `instance.admin_api`, `user.password_change_required`, `impersonation`)
- Modify: `web/src/routes/app.admin.users.tsx`, `app.admin.organizations.tsx`, `app.admin.sites.tsx` (`beforeLoad`: redirect to `/app/admin/settings` when `!me.instance.admin_api`), and the Admin nav (hide the three links)
- Modify: `web/src/features/admin/adminApi.ts` (`createUser`, `deleteOrganization`, `restoreOrganization`, `setSiteRetention`, `impersonate`)
- Create: `web/src/features/admin/CreateUserDialog.tsx`, `DeleteOrganizationDialog.tsx`, `ImpersonateDialog.tsx`
- Modify: `AdminUsersPage.tsx` ("Create user" button, "Log in as" row action), `AdminOrganizationsPage.tsx` (Delete, a "Deleted" filter, Restore, purge date), `AdminSitesPage.tsx` (retention select with 0/7/30/90/365, where 0 means "Forever")
- Modify: `web/src/messages/en.ts` and the `es`, `de` and `bn` locales
- Test: the matching `*.test.tsx` files

- [ ] **Step 1: Write the failing tests** (Vitest + Testing Library, with the API mocked).
  - Nav hides Users, Organizations and Sites when `admin_api` is false, and the route redirects.
  - `CreateUserDialog`:
    - The default mode posts without `password`.
    - "Set a password" mode posts `password`.
    - A `delivery: "link"` response shows the link with a copy button and the text "Send this link to the user. It works for 7 days."
  - `DeleteOrganizationDialog`: the Delete button stays disabled until the typed slug matches, then sends `confirm_slug`.
  - Deleted filter rows show "Purges on <date>" and a Restore button that posts restore.
  - Retention select change calls `PATCH /admin/sites/{id}` with `retention_days`.
  - `ImpersonateDialog`: a reason under 3 characters keeps the submit disabled. Success navigates to `/app`.
- [ ] **Step 2: Run** `cd web && bun run test src/features/admin/`. Expected: FAIL.
- [ ] **Step 3: Implement** with shadcn `Dialog`, `Select` and `Button`, following the existing Admin page patterns.
- [ ] **Step 4: Run** `bun run test && bunx tsc -b`. Expected: PASS, clean.
- [ ] **Step 5: Commit** `feat(web): manage users, orgs and retention from Admin`.

### Task 11: Impersonation banner and forced change-password screen

**Files:**
- Create: `web/src/components/ImpersonationBanner.tsx`, rendered by `AppShell` above all content when `me.impersonation` is set.
- Create: `web/src/features/account/ForcePasswordChange.tsx`. The app root renders it instead of the app when `me.user.password_change_required`. It holds a password form and a logout button.
- Test: `ImpersonationBanner.test.tsx`, `ForcePasswordChange.test.tsx`; e2e `web/e2e/admin-management.spec.ts`

- [ ] **Step 1: Write the failing tests.**
  - The banner text is "Viewing as <email> (by <admin email>, ends <HH:MM>)" with an "Exit" button that posts end and navigates to `/app/admin/users`. It has no dismiss control.
  - The forced screen renders only the form and logout. A successful change refetches `/me`, and the app renders.
  - e2e with the flag on:
    1. the admin creates a user (link mode), and the link is visible;
    2. the admin logs in as that user with a reason, and the banner is visible;
    3. Exit returns to Admin Users;
    4. the admin deletes the org (typing its slug), then restores it;
    5. the admin sets a site's retention to 90.
  - Screenshots at 1440 and 390, with no horizontal overflow.
- [ ] **Step 2: Run** the unit tests, and the e2e with `SUPERMETRY_ADMIN_API=true` added to the e2e server env. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `bun run test`, `bunx tsc -b`, and the e2e (`SUPERMETRY_TEST_DSN=… SUPERMETRY_E2E_PORT=… bunx playwright test e2e/admin-management.spec.ts`). Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): impersonation banner and forced password change`.

### Task 12: OpenAPI, docs and decision record

**Files:**
- Modify: `internal/api/openapi.yaml` (all new routes, the `admin_api_key` security scheme on the gated routes, and the new `/me` fields). Regenerate `web/src/lib/api.gen.ts` with the repo's generator script.
- Modify: `docs/guide/configuration.md` (the flag, the key behaviour, and the upgrade note), `SECURITY.md` (impersonation: who, how long, and where it's logged)
- Modify: `memory.md` (decision rows: the admin management switch, the key limits, impersonation rules)
- Modify: `progress.md` (a dated entry)

- [ ] **Step 1: Run** the OpenAPI lint or contract test already in the repo (`grep -rn openapi Makefile`). Expected: FAIL for the missing routes.
- [ ] **Step 2: Update** the spec and docs.
- [ ] **Step 3: Run** `go test ./internal/api/...` and `make api-inventory`. Expected: PASS.
- [ ] **Step 4: Commit** `docs: document the admin management API`. Push and open PR 5.
