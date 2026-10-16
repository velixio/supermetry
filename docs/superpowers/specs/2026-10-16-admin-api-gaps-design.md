# Admin management API: design

Date: 2026-10-16. Status: proposed, awaiting user approval.

## Intent

Supermetry Cloud runs one shared core for many customers. Its control plane manages customers through core's instance admin API and never writes core's database directly (Cloud design `supermetry-cloud/docs/superpowers/specs/2026-10-16-cloud-saas-design.md` §3). Most customer support already works that way. Four things do not:

1. Deleting an org (today only the org's owner can).
2. Setting a site's retention (the admin API can only read it).
3. Creating a user.
4. Logging in as a customer for support.

A fifth gap turned up while checking the code: the admin API accepts only a logged-in admin user. `SUPERMETRY_ADMIN_API_KEY` does not authorize `/api/private/admin/*` (`adminPrincipal`, `internal/platform/http/handlers_admin.go:21` requires a user ID), so the Cloud control plane cannot call it at all.

This work belongs in core, not in a separate project that writes core's database. Core caches sites, sessions and admin state in memory, and keeps rules in code: the last-owner and last-admin checks, ending sessions on suspend, audit, and purge reminders. A database writer outside core would miss the caches and duplicate the rules. (User, 2026-10-16: build it here, behind a switch.)

## Who sees what

A self-hoster runs one org or a few, and needs none of this. A host running Supermetry for other people needs all of it. So the customer-management part of the admin API sits behind one switch that is off by default.

**`SUPERMETRY_ADMIN_API`**, a boolean, defaults to `false` (user).

Behind the switch:

- Existing endpoints: list users, update a user (suspend, password reset, instance admin), list organizations, get an organization, list sites. (User: "user list also".)
- New endpoints, specified below: create a user, delete and restore an org, set a site's retention, and log in as a customer.

Always on, as today: admin settings, the 2FA policy and exemptions, the instance audit log, and updates.

When the switch is off:

- The gated endpoints answer `404` with the same body as an unknown route, so their existence is not revealed.
- `GET /api/private/me` reports `instance.admin_api: false`. The SPA hides the Admin Users, Organizations and Sites pages, and their routes redirect to the first Admin page that is still visible.
- The CLI (`supermetry user create`, `make-admin`, `reset-password`) keeps working. It is the self-hoster's path.

This takes the Users, Organizations and Sites pages away from existing self-hosted instances on upgrade, until the operator sets the switch. The release notes and `docs/guide/configuration.md` must say so in the same release.

## Machine access for the control plane

When `SUPERMETRY_ADMIN_API=true`, the gated endpoints also accept `Authorization: Bearer <SUPERMETRY_ADMIN_API_KEY>`. They accept it only when that key is set and at least 32 bytes long. The existing constant-time comparison is reused (`validAdminBearer`).

- A key request acts as a service principal with no user. Instance audit records it with the actor `admin_api_key`.
- A key request skips CSRF (as today), the per-user admin rate limit and the 2FA requirement. It gets its own rate limit of 600 requests a minute.
- Support actions are for human instance admins only (user, 2026-10-16: "for SaaS we need to debug, it's general support, but limited to admin users"). With the key:
  - logging in as a customer is **never** allowed (see below);
  - granting or removing instance admin (`instance_admin` in `PATCH /admin/users/{user}`) answers `403 admin_key_forbidden`;
  - creating a user with `password` answers `403 admin_key_forbidden`, so the key can only use the set-password link.

  This closes a path where the key could create a user with a known password, make them an admin, then log in and impersonate anyone. Tests must prove each of these refusals.
- The always-on admin endpoints keep working as today. The key does not open them (designer's call).
- The service layer takes an actor, `Actor{UserID int64; AdminKey bool}`, instead of a bare `actorID`. `requireAdmin` passes for `AdminKey` and checks `is_admin` for users, as today. *Designer's call.*
- The 600-a-minute key limit is one shared bucket per instance, whoever the caller is. *Designer's call.*

## 1. Delete and restore an org

The user picked a soft delete with a grace period. Core already has one: D62 (`memory.md`), restorable deletion with a 7-day grace, a notice email, a reminder 72 hours before the purge, and the `site-erasure` purge job. Cloud already keeps a suspended org for 30 days before it deletes it (Cloud design §4). So admin delete reuses D62 and does not build a second soft delete: Cloud's 30 days, then core's 7. (Proposed to the user 2026-10-16. The user answered "continue", taken as accepting.)

- `DELETE /api/private/admin/organizations/{org}`, body `{"confirm_slug": "<slug>"}`.
  - A missing or wrong slug answers `422`.
  - An org that is already deleted answers `409`.
- The org-level part of `Services.DeleteOrg` (`private.go:~443`) moves into a function both the owner path and the admin path call. The admin path skips the membership and current-org checks. Everything else is the same:
  - The org's sites get `deleted_at` and `deleted_with_org`.
  - The site registry is invalidated, so ingest stops at once.
  - An org with only a demo site or nothing is erased at once.
  - Notice and reminder emails go to the owners.
- `POST /api/private/admin/organizations/{org}/restore`. It reuses `orgs.Restore` without the owner-membership check, and keeps the same errors: `409 not_restorable` and `409 site_limit`.
- Admin list and get include `deleted_at` and `purge_at` for deleted orgs. `GET /api/private/admin/organizations?state=deleted` lists the restorable ones.
- Audit:
  - Instance audit gets `instance.organization.deleted` and `instance.organization.restored`.
  - The org's own audit gets `organization.deleted` with the actor shown as the instance admin, or as `admin_api_key` for the key.

## 2. Set a site's retention

- `PATCH /api/private/admin/sites/{site}`, body `{"retention_days": 0|7|30|90|365}`. This is the same field and the same validation as the owner path (`retentionClass`, `sitesvc/sites.go:616`).
- Only retention can be changed here. Any other field in the body answers `422`.
- Shortening retention takes effect through the existing retention job. No data is deleted inside the request.
- Audit: instance audit gets `instance.site.retention_changed` with the old and new values. The site's org audit gets the same.

## 3. Create a user

`POST /api/private/admin/users`:

```json
{
  "email": "a@example.com",
  "name": "optional",
  "org": { "id": 12, "role": "admin" },   // or { "new": "Acme" }; role defaults to owner for a new org
  "password": "optional"
}
```

- With `{ "new": "Acme" }`, core creates the org the way signup does: the same slug generation, with a numeric suffix on a collision. `signup_mode` does not apply to admin creation. The response includes `org: { id, slug, name }` for both forms, so Cloud can make its next calls. *Designer's call.*
- The email must be unused (`409`). The role must be a valid org role, and `org.id` must exist and must not be deleted.
- **Default: a set-password link (user).** Core creates the user with an unusable password, adds the membership, and issues a set-password token.
  - The token is valid for **7 days**. Password-reset tokens stay at 1 hour. (`password_resets` gains a per-row `expires_at`, or a `purpose` column; the builder picks one.)
  - When mail is enabled, core sends a "Set your Supermetry password" email and the response is `{ "user": …, "delivery": "email" }`.
  - When mail is disabled, the response is `{ "user": …, "delivery": "link", "link": "<url>" }` so the admin can send it by hand. The link appears only in this response and is never logged.
- **Option: the admin sets a password (user).**
  - It must pass the normal password rules.
  - The user is flagged `password_change_required`, a **new** column. This is a different state from today's `password_reset_required`, which blocks login entirely (`login.go:165`).
  - With the new flag, login succeeds, but the session can only do three things: `GET /api/private/me`, `POST /api/private/account/password` and logout. The password change comes before any 2FA enrollment the 2FA policy requires. *Designer's call.* Everything else answers `403 password_change_required`, and the SPA shows a forced change-password screen. Changing the password clears the flag.
- Audit: instance audit gets `instance.user.created`, with the method (`link` or `password`), never the password or the link. The org audit gets `member.added`.

## 4. Log in as a customer

The user picked full access with a time limit.

**Starting it**

- `POST /api/private/admin/users/{user}/impersonate`, body `{"reason": "ticket #123"}`. A reason is required: 3 to 200 characters.
- Only a human instance admin with a normal session can start it. The admin API key cannot, and neither can a session that is itself impersonating.
- These targets are refused: instance admins, disabled or deleted users, and users who still have `password_reset_required` or `password_change_required` set.
- Core creates a `user_sessions` row for the target with `impersonator_user_id`, `impersonation_reason` and a hard expiry **30 minutes** after creation. Idle extension never pushes it past that.
- The admin's own session cookie is moved to a second cookie, `supermetry_admin_return`. It is HttpOnly, Secure when the request is secure, SameSite=Strict and path `/`. The impersonation session takes `supermetry_session`.

**Ending it**

- `POST /api/private/auth/impersonation/end` deletes the impersonation session and restores the admin's cookie.
- When the impersonation session expires, the next request falls back to the return cookie, and the SPA lands the admin back in Admin.

**While it lasts**

- `GET /api/private/me` includes `impersonation: { by: {id, email}, started_at, expires_at, reason }`. The SPA shows a fixed banner with the text "Viewing as <user> (by <admin>, ends 14:32)" and an "Exit" button. The banner cannot be dismissed.
- Access is full (user), with one class of exceptions. Actions that would outlast the 30 minutes or take over the account answer `403 impersonation_forbidden`:
  - changing the password, email or 2FA;
  - creating API keys or personal tokens;
  - revoking the user's other sessions;
  - deleting the account.

  *This is the designer's call.* It keeps "time-limited" true. Everything else, including org and site settings, is allowed.
- **The customer does not see it (user, 2026-10-16).**
  - The impersonation session is left out of the customer's Account → Sessions page and of any "new sign-in" notice.
  - Org audit entries written during impersonation show the customer as the actor. The org audit view never shows `impersonator_user_id`.
- **Admins see everything.**
  - Every audit entry written during impersonation stores `impersonator_user_id`.
  - The instance audit log shows those entries as "<admin> as <user>", with the reason.

**Ending early.** On every request the session check re-validates the impersonator: they must still be an enabled instance admin, their return session must still exist, and the switch must still be on. If any of that fails, the impersonation session is deleted and the request is treated as logged out. Suspending the target already deletes all of the target's sessions. Deleting the target's org does not end the session. *Designer's call.*

**Audit:** only the instance audit gets `support.impersonation.started` and `support.impersonation.ended`, with the reason. The customer's org audit does not.

## UI in core (when the switch is on)

These are small additions to the existing Admin pages:

- **Users:** a "Create user" dialog with email, name, org (existing or new), role, and "Send a set-password link" (the default) or "Set a password". There is also a "Log in as" action with a required reason.
- **Organizations:** "Delete" (type the slug to confirm). Deleted orgs show their purge date and a "Restore" button.
- **Sites:** a retention select.
- **Everywhere:** the impersonation banner, and the forced change-password screen.

## Goals, non-goals, anti-goals

Goals:

- The Cloud control plane can do every action in the Cloud design's admin table through core's API, with a machine key.
- A self-hoster who never sets the switch gets a smaller admin area and no new attack surface.

Non-goals:

- Cloud's admin console. That is built in `supermetry-cloud`.
- Plans, limits and billing.
- Bulk operations.
- A longer core grace period than D62's 7 days.

Anti-goals, which would mean failure even if the feature works:

- An impersonation session that outlives 30 minutes or can lock the real user out.
- Any path where the admin API key logs in as a user.
- A set-password link or an admin-set password in logs or in the audit trail.
- The gated endpoints answering anything but `404` while the switch is off.

## Constraints

- Go and `net/http`, `pgx`, forward-only transactional migrations.
- No database read on the ingest hot path (unchanged).
- Tests first for: the switch (`404` when off), key authorization, delete and restore through the admin path, retention validation, both create-user flows, and impersonation (start, expiry, blocked actions, return cookie, audit).
- Add every new endpoint to `internal/api/openapi.yaml`.
- Licensing: no code from AGPL or EUPL references.

## Audit storage

*Designer's call.* `audit_log` gains two columns:

- `impersonator_user_id`: nullable, references users.
- `actor_admin_key`: a boolean, default false.

The existing check that at most one of `actor_user_id` and `actor_key_id` is set stays. `actor_admin_key` is true only when both are null. Instance and org audit writers take the `Actor` and fill these columns. The instance audit view shows "Admin API key" or "<admin> as <user>". Org audit views show the customer, never the impersonator.

## Left to the builder

- Migration shape: the `password_resets` purpose or expiry column, `users.password_change_required`, and `user_sessions.impersonator_user_id` and `impersonation_reason`.
- Exact rate-limit numbers within reason.
- Email template wording, matching the existing notices.
- How the SPA routes guard on `instance.admin_api`.
