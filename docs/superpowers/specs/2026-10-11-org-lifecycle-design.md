# Organization and site lifecycle: design

Status: accepted (user, 2026-10-11). Plan: [../plans/2026-10-11-org-lifecycle.md](../plans/2026-10-11-org-lifecycle.md).

## 1. Goal

- Deleting a real site, or a real organization, can be undone for **7 days**. After that, its data is erased for good.
- Owners can delete an organization from the UI and rename it.
- When SMTP is configured, people get an email when a deletion is scheduled and a reminder **72 hours** before the final erase.
- Demo projects (`sites.is_demo`) are never held: they are erased right away, and no email is sent.

## 2. Current state (2026-10-11)

- **Site delete:** `sitesvc.Sites.Delete` soft-deletes (`deleted_at`). The `site-erasure` job (`cmd/supermetry/serve.go` `eraseDeletedSites`, every minute) erases every tombstoned site right away and sets `settings.erased_at`. It re-processes already erased sites every minute. There is no restore.
- **Org delete:**
  - `DELETE /api/private/orgs/{org}` (owner, password/TOTP check) soft-deletes the org and all its live sites in one transaction (`Services.DeleteOrg`, `internal/platform/http/private.go`).
  - There is no UI and no restore. Nothing renames an org.
- **Mail:** `mail.Sender` with `Enabled()`, which is false when no SMTP host is set; `cfg.BaseURL` builds links.

## 3. Data model

Migration `0045_deletion_grace.sql` (main owns `0040` and the demo branch owns `0041`–`0044`; merge the demo branch first):

```sql
ALTER TABLE sites ADD COLUMN deleted_with_org boolean NOT NULL DEFAULT false;
ALTER TABLE sites ADD COLUMN deletion_notified_at timestamptz;
ALTER TABLE sites ADD COLUMN deletion_reminded_at timestamptz;
ALTER TABLE sites ADD COLUMN erased_at timestamptz;
UPDATE sites SET erased_at = (settings->>'erased_at')::timestamptz WHERE settings ? 'erased_at';
ALTER TABLE orgs ADD COLUMN deletion_notified_at timestamptz;
ALTER TABLE orgs ADD COLUMN deletion_reminded_at timestamptz;
ALTER TABLE orgs ADD COLUMN erased_at timestamptz;
```

- **Grace:** a deleted site or org is *scheduled*. Its purge time is `deleted_at + 7 days` (`const DeletionGrace = 7 * 24 * time.Hour`, in `internal/platform/lifecycle`).
- **Existing tombstones:** sites already tombstoned before this migration keep their current behavior. They are already erased or will be erased by the next job run, because their `deleted_at` is in the past.
- **Erase flag:** `erased_at` moves from `settings` to a real column. Keep writing `settings.erased_at` too, for anything still reading it.

## 4. Behavior

### 4.1 Sites

- **Delete (owner/admin, as today):** sets `deleted_at` and writes audit `site.deleted`. Ingest stops at once, because `ActiveSites` already filters deleted sites.
  - A demo site is erased in the next erasure run (no grace).
  - For a real site, the deletion email is queued (see 4.4).
- **Restore:** `POST /api/private/sites/{site}/restore` (owner/admin).
  - Allowed while `deleted_at > now - 7d`, `erased_at IS NULL`, the site is not a demo, and its org is not deleted. Otherwise 409 `not_restorable`.
  - Clears `deleted_at`, `deleted_with_org`, `deletion_notified_at` and `deletion_reminded_at`; writes audit `site.restored`; invalidates the registry.
  - Data collected before the delete is intact. Hits sent during the grace period were dropped.
- **List:** `GET /api/private/orgs/{org}/sites/deleted` (owner/admin) returns the scheduled sites with `deleted_at` and `purge_at`.
- **Erasure job:** erases a site when it is `erased_at IS NULL` and either:
  - it is a demo, or
  - `deleted_at <= now - 7d`.

  It sets `erased_at`. It never touches a site that is already erased.

### 4.2 Organizations

- **Delete (owner, password/TOTP, as today):**
  - **The org has at least one live non-demo site:** *scheduled*. The org gets `deleted_at`. Its live sites get `deleted_at` (the same timestamp) and `deleted_with_org = true`. Write audit `organization.deleted` with `{scheduled: true}`, and queue the email.
  - **Otherwise** (no sites, or only a demo): deleted immediately. The org gets `deleted_at`, and so do its sites. The org's `erased_at` is set as soon as its sites are erased. No email.
- **Restore:** `POST /api/private/orgs/{org}/restore`.
  - Allowed for a user who holds an **owner** membership in that org (memberships survive the soft delete), while `deleted_at > now - 7d` and `erased_at IS NULL`.
  - Restores the org, plus every site with `deleted_with_org = true`. Clears the notice columns and writes audit `organization.restored`.
  - Sites deleted individually before the org delete stay deleted, with their own grace.
  - Authenticating as a member of a deleted org fails today. The restore and list endpoints must work through the **account** (user) scope, not the org scope.
- **List:** `GET /api/private/me/orgs/deleted` returns the deleted, restorable orgs where the user is an owner, with `deleted_at` and `purge_at`.
- **Purge:** once `deleted_at <= now - 7d`, the erasure job erases each site of the org (as in 4.1). It then sets the org's `erased_at`. Rows are not hard-deleted.
- **Rename:** `PATCH /api/private/orgs/{org}` with `{ "name": string }` (owner/admin).
  - Trimmed, 1–100 characters, no control characters. The slug is unchanged.
  - Writes audit `organization.renamed` with `{from, to}`.

### 4.3 No-organization state

When `/me` has no memberships, `/app` shows "You don't belong to an organization" with:

- a **Create organization** button, linking to the existing new-org page;
- the restorable orgs list.

### 4.4 Emails (only when `mail.Sender.Enabled()`)

A scheduler job, `deletion-notices`, runs every 5 minutes, using the scheduler's advisory lock.

- **Site, scheduled notice:**
  - **When:** `deleted_at` is set, the site is not a demo, `deleted_with_org = false`, `deletion_notified_at` is null, and `erased_at` is null.
  - **Recipients:** the org's active owners and admins.
  - **Subject:** "{site} is scheduled for deletion on {date}".
- **Site, reminder:** sent when `deletion_reminded_at` is null and `now >= deleted_at + 7d - 72h`, under the same conditions. Subject: "Reminder: {site} will be deleted in 3 days".
- **Org, scheduled notice and reminder:** the same rules on `orgs`, sent to the org's owners. The body lists the sites that will be deleted with it. Sites with `deleted_with_org = true` get no separate email.
- **Body:** plain text, saying what will be deleted, when (UTC date and time), and how to restore. The restore link is `{BaseURL}/app/{org}/sites` for sites, and `{BaseURL}/app/account` for orgs.
- **Delivery:** set `*_notified_at` / `*_reminded_at` after every recipient's send succeeds. On a send error, log it (site/org id only, never an address) and leave the column null, so the next run retries. If mail is disabled, the job does nothing and the columns stay null, so turning SMTP on later sends any notices that are still relevant (skip the "scheduled" notice once the reminder window has started; send only the reminder).
- **Clock:** the job uses an injected clock, for tests.

## 5. API and UI

- OpenAPI covers every new route. `deleted_at` and `purge_at` appear only on deleted-item lists and delete results, not on live site or organization objects.
- **Sites page:** a "Scheduled for deletion" section listing each site with "Deletes on {date}" and a **Restore** button. The delete dialog text becomes: "The site stops collecting data now. You can restore it for 7 days; after that its data is permanently deleted." Demo sites keep the immediate wording: "The demo project and its data are deleted now."
- **Org settings (general):**
  - a **Name** field with Save (owner/admin);
  - a **Danger zone** with **Delete organization** (owner): confirm dialog, password/TOTP prompt (reuse the existing sensitive-action UI), and wording that depends on whether real projects exist ("scheduled for 7 days" against "deleted now").
- **Account page:** "Organizations scheduled for deletion", with Restore buttons.
- **Switcher:** org names update after a rename (invalidate the query).

## 6. Testing

- **Store and service:**
  - grace selection and the "already erased" skip;
  - restore rules: inside grace, after grace, erased, demo, deleted org;
  - org delete for each branch (real sites → scheduled; demo/no sites → immediate);
  - org restore brings back only `deleted_with_org` sites;
  - rename validation;
  - audit rows.
- **Erasure job,** with a fake clock:
  - a real site is kept at 6 days 23 hours and erased at 7 days 1 minute;
  - a demo is erased immediately;
  - an erased site is never processed again.
- **Notices,** with `mail.Fake` and a fake clock:
  - the scheduled email goes to the right recipients once;
  - the reminder is sent once at 72 hours before;
  - none is sent for a demo, or for `deleted_with_org` sites;
  - a restore clears the state and stops the reminder;
  - a send failure retries next run;
  - mail disabled sends nothing;
  - mail enabled late sends only the reminder when the window has started.
- **HTTP:** authz for every route (viewer 403, other org 404, admin vs owner), 409 `not_restorable` cases, and restore through account scope for a deleted org.
- **Web:** the sites-page section and restore, the rename form, the delete-org flow (both wordings), the no-org screen, and the account restore list.
