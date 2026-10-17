# Instance Admin API Additions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an instance admin, or an automation holding the admin API key, delete an org, set a site's retention, create a user, and sign in as a user for support. All of this goes through `/api/private/admin/*`.

**Architecture:** Five general instance-admin features, all off unless an instance admin or the admin key uses them. They reuse the existing services: `DeleteOrg`'s transaction body, `Sites.Update` validation, `users.CreateWithDB`, and `auth.createSession`. Each is exposed through a new admin route in `internal/platform/http/handlers_admin.go` and writes an instance audit entry with `store.WriteInstanceAudit`. A bearer `SUPERMETRY_ADMIN_API_KEY` becomes a valid instance-admin principal, for these routes only.

**Tech Stack:** Go, `net/http`, pgx, oapi-codegen (`go generate ./internal/api`), Postgres migrations in `internal/db/migrations`.

**Spec:** the requirements are self-contained in this plan (user decision 2026-10-16: add these as general admin features in core).

## Global Constraints

- General features only: no Cloud, plan or billing names in code, routes, docs or comments.
- Every mutating admin route writes `store.WriteInstanceAudit` in the same transaction as the change.
- Retention values: only `0, 7, 30, 90, 365` (core retention classes).
- Org deletion through admin uses the same 7-day restorable deletion as owners (`lifecycle.DeletionGrace`) and the same notice emails. Demo-only and empty orgs are erased at once, as today.
- Impersonation sessions expire after **1 hour**, cannot be extended, and cannot use the admin routes.
- The admin key never works outside `/api/private/admin/*`.
- Spec drift test `openapi_drift_test.go` must pass: every route goes in `internal/api/openapi.yaml`, followed by `go generate ./internal/api` and `cd web && bun run gen:api`.
- Work on branch `feat/instance-admin-api`; merge via PR. Each task is one or more commits that build and vet.

## Review Focus

1. **The admin key sent to a non-admin route** (`/api/private/orgs/{org}`, the public API in non-compat mode): it must still get 401, never a principal. *(Task 1 test.)*
2. **Deleting an org that is already deleted, or doesn't exist:** expect 404, no second audit row, and no extra notice email. *(Task 2 test.)*
3. **Creating a user whose email already exists**, including in a different case: expect 409, and no password is changed. *(Task 4 test.)*
4. **An impersonated session calling an admin route, or impersonating an instance admin:** both get 403. *(Task 5 test.)*
5. **Retention set to a value outside the classes, e.g. 60:** expect 400, and the site is unchanged. *(Task 3 test.)*

---

### Task 1: Admin API key authenticates admin routes

**Files:**
- Modify: `internal/platform/http/handlers_admin.go:21` (`adminPrincipal`)
- Modify: `internal/platform/http/private.go` (pass `AdminAPIKey` into the handler deps it already has, `private.go:155`)
- Create: `internal/db/migrations/0230_admin_service_actor.sql`
- Test: `internal/platform/http/handlers_admin_key_test.go`

**Interfaces:**
- Produces: `func (h *handler) adminPrincipal(w, r) (authz.Principal, bool)`. It now accepts `Authorization: Bearer <AdminAPIKey>` (constant-time compare, reusing `validAdminBearer`) and returns `authz.Principal{UserID: h.deps.AdminServiceUserID}`.
- Produces: `Deps.AdminServiceUserID int64`, loaded once at startup with `store.AdminServiceUserID(ctx, db) (int64, error)`. The migration inserts a disabled, password-less system user (email `admin-key@system.invalid`), which can't log in, so audit rows have a valid `actor_user_id`.
- Requires `SUPERMETRY_ADMIN_API_KEY` to be at least 32 characters; otherwise key auth is off and startup logs a warning.

- [ ] **Step 1: Write failing tests** `TestAdminKeyListsUsers` (bearer key → GET `/api/private/admin/users` 200; the audit actor for a later PATCH is the system user), `TestAdminKeyRejectedOutsideAdmin` (same key → GET `/api/private/orgs/1` 401), `TestAdminKeyWrongKey` (401), `TestAdminKeyShortKeyDisabled` (a 16-character key configured → 401).
- [ ] **Step 2: Run** `SUPERMETRY_TEST_DSN=$DSN go test ./internal/platform/http/ -run AdminKey`. Expected: FAIL.
- [ ] **Step 3: Implement** the migration (the system user, `disabled = true`, password hash null or unusable) and the bearer branch in `adminPrincipal`, before the cookie path. Apply the same per-actor rate limit.
- [ ] **Step 4: Run** the step 2 command, then `-run Admin`. Expected: PASS, including the existing `TestAdminRouteAuthorizationMatrix`.
- [ ] **Step 5: Commit** `feat(admin): accept the admin API key on instance admin routes`

### Task 2: Admin deletes an organization

**Files:**
- Modify: `internal/platform/http/private.go:443-503`. Extract the transaction body of `DeleteOrg` into `deleteOrgTx`.
- Modify: `internal/platform/http/handlers_admin.go`, `private.go:243-257` (route)
- Modify: `internal/api/openapi.yaml` (`DELETE /api/private/admin/organizations/{org}`, 204 / 404)
- Test: `internal/platform/http/admin_org_delete_test.go`

**Interfaces:**
- Produces: `func deleteOrgTx(ctx context.Context, tx pgx.Tx, orgID int64, now time.Time) (deletedSites []int32, err error)`. Shared by the owner and admin paths. Each caller keeps its own audit: owner → `audit.Write("organization.deleted")`; admin → `WriteInstanceAudit` with Action `"instance.organization.deleted"`, TargetType `"organization"`.
- Produces: `func (s Services) AdminDeleteOrg(ctx context.Context, actorUserID int64, orgID int64) error`, which returns `store.ErrNotFound` for a missing or already deleted org.

- [ ] **Step 1: Write failing tests** `TestAdminDeleteOrgRestorable` (org with a real site → 204; `orgs.deleted_at` set; the owner can restore via `POST /api/private/me/orgs/{org}/restore` within 7 days), `TestAdminDeleteOrgDemoOnlyErasedNow`, `TestAdminDeleteOrgTwice404` (second call 404, exactly one audit row), `TestAdminDeleteOrgNonAdmin403`, and `TestOwnerDeleteOrgUnchanged` (existing owner path still passes `organization_delete_test.go`).
- [ ] **Step 2: Run** `go test ./internal/platform/http/ -run 'DeleteOrg'`. Expected: new tests FAIL, old tests PASS.
- [ ] **Step 3: Implement** the extraction (no behavior change to the owner path), `AdminDeleteOrg`, the handler and route, and the spec entry. Then run `go generate ./internal/api` and `cd web && bun run gen:api`.
- [ ] **Step 4: Run** step 2 plus `-run OpenAPIDrift`. Expected: PASS.
- [ ] **Step 5: Commit** in two commits: `refactor(orgs): share the org deletion transaction` and `feat(admin): delete organizations from the instance admin API`.

### Task 3: Admin sets a site's retention

**Files:**
- Modify: `internal/platform/sitesvc/sites.go:207`. Add an admin variant that skips the org-scoped principal check but keeps `validateSite` and `retentionClass` (`sites.go:560,616`).
- Modify: `handlers_admin.go`, `private.go` (route), `openapi.yaml` (`PATCH /api/private/admin/sites/{site_id}` with body `{retention_days}`, enum `[0,7,30,90,365]`)
- Test: `internal/platform/http/admin_site_retention_test.go`

**Interfaces:**
- Produces: `func (s *Sites) AdminSetRetention(ctx context.Context, actorUserID int64, siteID int32, days int) (store.Site, error)`. Applies the same class move as an owner edit and writes `WriteInstanceAudit` with Action `"instance.site.retention_changed"`, Metadata `{from, to}`.

- [ ] **Step 1: Write failing tests** `TestAdminSetRetention` (30 → 365 returns 200; `AdminSite.retention_days` = 365 in GET `/api/private/admin/sites`; one audit row with `{from:30,to:365}`), `TestAdminSetRetentionInvalid` (60 returns 400, site unchanged), `TestAdminSetRetentionDeletedSite404`.
- [ ] **Step 2: Run** `go test ./internal/platform/... -run AdminSetRetention`. Expected: FAIL.
- [ ] **Step 3: Implement** `AdminSetRetention`, the handler, the route and the spec entry, then regenerate the code.
- [ ] **Step 4: Run** step 2 plus `-run OpenAPIDrift`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(admin): set site retention from the instance admin API`

### Task 4: Admin creates a user

**Files:**
- Modify: `handlers_admin.go`, `private.go` (route), `openapi.yaml` (`POST /api/private/admin/users`, body `{email, name, org_name?, send_reset?}` → 201 `AdminUser`, 409 on a duplicate email)
- Modify: `internal/platform/admin/admin.go` (service method)
- Test: `internal/platform/admin/admin_create_user_test.go`, `internal/platform/http/admin_create_user_test.go`

**Interfaces:**
- Consumes: `users.CreateWithDB(ctx, tx, email, name, password, now)` (`platform/users/users.go:32`), `authService.StartReset` (as in the CLI `cmd/supermetry/user.go:40`).
- Produces: `func (a *Admin) CreateUser(ctx context.Context, actorUserID int64, in CreateUserInput) (AdminUser, error)` with `type CreateUserInput struct { Email, Name, OrgName string; SendReset bool }`. Creates the user with a random unusable password. When `OrgName` is not empty, also creates the org with the user as owner, in the same transaction as the CLI path. When `SendReset` is set, emails a password-set link. Audit Action `"instance.user.created"`.

- [ ] **Step 1: Write failing tests** `TestAdminCreateUserWithOrg` (201; user is owner of a new org; reset email queued when SMTP is configured), `TestAdminCreateUserNoOrg` (user has no memberships), `TestAdminCreateUserDuplicateEmail` (`A@x.test` vs existing `a@x.test` → 409; existing user's password hash unchanged), `TestAdminCreateUserBadEmail` (400).
- [ ] **Step 2: Run** `go test ./internal/platform/... -run AdminCreateUser`. Expected: FAIL.
- [ ] **Step 3: Implement** the service method, the handler, the route and the spec entry, then regenerate the code. Factor out the CLI's org-creation helper if needed, rather than copying it.
- [ ] **Step 4: Run** step 2 plus `-run OpenAPIDrift`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(admin): create users from the instance admin API`

### Task 5: Admin signs in as a user (impersonation)

**Files:**
- Create: `internal/db/migrations/0231_session_impersonation.sql` (`ALTER TABLE user_sessions ADD COLUMN impersonated_by bigint REFERENCES users(id)`, plus `ADD COLUMN hard_expires_at timestamptz`)
- Modify: `internal/platform/auth/login.go:259` (export a variant), `handlers_authenticator.go:29-79` (carry the flag), `handlers_admin.go:21` (reject impersonated sessions), the `me` handler and the `openapi.yaml` `Me` schema (`impersonated_by_email`, nullable)
- Modify: `openapi.yaml`: `POST /api/private/admin/users/{user}/impersonate` → 204 and sets the session cookies
- Modify: `web/src` (one banner in the org layout shell when `me.impersonated_by_email` is set: "Signed in as <email> by an administrator · End session", calling logout)
- Test: `internal/platform/http/admin_impersonate_test.go`, `web/src/**/impersonation-banner.test.tsx`

**Interfaces:**
- Produces: `func (a *Auth) CreateImpersonationSession(ctx context.Context, adminUserID, targetUserID int64, now time.Time) (token string, err error)`. The session lasts 1 h with `hard_expires_at = now + 1h`, never extended by activity, and `impersonated_by = adminUserID`. Audit Action `"instance.user.impersonated"`, written to the instance audit and also to the target's orgs' audit (`audit.Write`), so org owners can see it.
- Requires sensitive-action verification (`Auth.VerifySensitiveAction`) for cookie sessions. For the admin key, the response is `200 {token, expires_at}` instead of setting cookies, so a front service can hand the cookie to a browser.

- [ ] **Step 1: Write failing tests** `TestImpersonateCreatesShortSession` (cookie set; `me.impersonated_by_email` = admin email; session invalid after 1 h with a fake clock even with activity), `TestImpersonateAdminForbidden` (target is an instance admin → 403), `TestImpersonatedSessionCannotUseAdmin` (403 on GET `/api/private/admin/users`), `TestImpersonateDisabledUser403`, `TestImpersonateAuditBoth` (instance and org audit rows), `TestImpersonateWithKeyReturnsToken`. Web: `impersonation banner shows the target email and ends the session`.
- [ ] **Step 2: Run** `go test ./internal/platform/... -run Impersonat` and `cd web && bun run test impersonation`. Expected: FAIL.
- [ ] **Step 3: Implement** the migration, the exported session function, authenticator checks (hard expiry, the impersonation flag on the principal), the admin guard, the route, the `me` field and the banner. Then regenerate the code.
- [ ] **Step 4: Run** step 2, then the full gate: `gofmt -l . | grep -v knowledge`, `go vet ./...`, `make test`, `cd web && bun run typecheck && bun run test && bun run build`. Expected: all clean or PASS.
- [ ] **Step 5: Commit** in three commits: migration, Go (`feat(admin): sign in as a user for support`), web (`feat(web): show a banner during admin sign-in as a user`).

### Task 6: Docs and PR

- [ ] **Step 1:** Add the five routes to the self-hosting docs section on instance administration (`docs/`, wherever admin API docs live; find them with `grep -rl "admin/users" docs`). Add a CHANGELOG entry under Unreleased.
- [ ] **Step 2:** Scan for emails (`git diff main --stat` plus the email grep from CLAUDE.md), push `feat/instance-admin-api`, and open a PR (no AI attribution).
