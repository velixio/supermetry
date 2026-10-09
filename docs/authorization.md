# Organization and project authorization

Supermetry continues to use organizations and sites. A site is the project boundary;
the foundation does not introduce a second project database or membership system.
All resource lookups must check the owning organization and allowed site before
checking an operation's capability. `authz.Can` alone cannot establish ownership
from an integer site ID: callers must resolve and verify the resource first.

## Presets and additional grants

| Preset | Ordinary project access | Profiles, recordings, exports | Organization administration | Billing |
|---|---|---|---|---|
| Owner | All sites | Included | Members, keys, audit, transfer and deletion | Included |
| Admin | All sites | Included | Members except ownership changes, keys and audit | Separately assigned |
| Editor | Allowed sites; existing analysis/settings edits | Explicit grants | No member/key administration | Separately assigned |
| Viewer | Analytics on allowed sites | Explicit grants | No member/key administration | Separately assigned |

Members may additionally receive `profiles.read`, `replay.read` and `data.export`.
These are separate: profile access does not authorize recordings, and exporting
does not authorize reading otherwise-protected data. Unknown capability names are
rejected. Existing ordinary capabilities, including managing one's own aggregate
email-report subscriptions, keep their existing role behavior.

Billing Admin is an independent `billing_admin` assignment. Only an Owner may
change this assignment. An Admin cannot grant themselves billing authority.
A billing-only API assignment combines Viewer, no sites (`site_ids: []`), and
`billing_admin: true`. This does not permit analytics, profiles, recordings, exports, or member
management. This release establishes billing authorization only; it does not
add payment-provider checkout or a separate billing service. The open-source UI
hides billing cards, badges, copy and grant controls. It omits billing fields from
member edits and invitations, preserving existing grants on unrelated edits.

## Scope and API payloads

- `site_ids: null`: all current and future sites of this organization.
- `site_ids: []`: no sites, including when the member is a Billing Admin.
- A nonempty array: exactly those sites, all validated against the organization.
- Omitted fields in a membership update retain their current values. Explicit
  empty `capabilities` clears additional grants. `billing_admin: false` revokes
  that assignment. Existing base `role` values are unchanged.
- Owner/Admin have organization-wide site access; per-site restrictions belong
  to Editor/Viewer and API keys. Their preset grants cannot be narrowed by
  omitting an additional grant.

Membership and invitation responses include the assignments. The authenticated
membership response also includes `effective_capabilities`, computed by the
central policy for UI presentation. Clients cannot confer access by sending that
field; the server enforces authorization independently.

Invitations cannot grant Owner. Grant validation and acceptance are transactional;
acceptance rechecks the inviter's current authority, expiration, revocation,
single-use state and recipient email. Membership changes preserve the locked-org
last-owner guard. Session authentication reloads membership grants, so removal or
downgrade affects subsequent requests without waiting for session expiry.

## Keys, shares and delayed delivery

API keys have explicit scopes described in [API authentication](api-auth.md).
Sensitive scopes are `profiles:read`, `replay:read`, and `data:export`.
`stats:read` alone never implies them. Key creation validates the live issuer,
organization, scopes and sites; delegated keys cannot widen scope/site/expiry or
delegate `keys:manage`. A restricted `sites:write` key cannot create a site outside
its restriction or delete another site. Key managers cannot inspect or revoke a
broader key. Keys remain organization-owned when their human creator leaves;
revoke the key itself when an integration should lose access.

Shared links remain revocable, single-site aggregate dashboards. They do not
inherit the creator's sensitive capabilities and cannot export. Password cookies
are bound to the particular link. Revocation is checked on each request.

CSV permissions apply whether negotiated by `Accept: text/csv`, `format=csv`,
or the streaming export path, as well as the dedicated ZIP export. APIs redact
profile identity and replay metadata without the corresponding read grant.
Recorded assets must also match the authorized site/session. Aggregate email
reports recheck current subscription and site access after rendering and just
before delivery, including membership changes while a report was queued.

## Upgrade notice

This release deliberately tightens sensitive access. Owner/Admin retain these
operations through their presets. Editor/Viewer need explicit grants. Existing
stats-only keys and shared links are **not silently upgraded**. Inventory scripts
and users that legitimately need profile/recording/export access, grant the
minimum additional permissions, and test before deploying.

Collection/privacy settings and environment overrides remain independent ceilings.
A capability never enables collection or overrides a disabled recording service.
No new provider credentials, checkout, hosted service, or deployment is implied.

## Account navigation and sessions

The header contains Settings for the current organization and permitted current
site, plus Account with the signed-in identity, Account settings and Log out.
Settings pages retain their existing deep links and local navigation. Account
pages work with zero, one or multiple memberships; self-service requests resolve
the user session without attaching organization grants. Tenant requests still
require a valid membership and explicit organization when ambiguous.

Logout revokes only the presented session under CSRF protection. Missing,
expired or already-revoked sessions complete idempotently; database failures do
not clear the cookie or claim success. The client hides private content, cancels
requests and uploads, clears query/mutation state, suppresses late responses and
replaces the location with `/login` after confirmation. Failures offer retry or a
return to the app. Other tabs react to session changes; theme preference remains.

Issued `/invite?token=...` URLs redirect without caching to the existing
`/invite/$token` application route. The invitation's validation and acceptance
contract remains unchanged.

## Organization deletion lifecycle

Only a current organization Owner may delete an organization. The server locks
the organization and rechecks that role inside the deletion transaction, then
sets `deleted_at` on the organization and every active child site and records the
number of newly tombstoned sites in the audit entry. A concurrent site creation
either commits before that transaction and is tombstoned with the other sites,
or observes the deleted organization and is denied.

The successful response means access is disabled and the child sites are queued
for the existing site-erasure job; it does not mean every analytics row or replay
blob has already been removed. Membership sessions lose tenant access, API keys
stop authenticating, shared dashboards stop resolving, and scheduled reports
stop delivering. Their database rows remain until their existing lifecycle or
cascade removes them.

The site-erasure job retries tombstoned sites on every run and during graceful
shutdown. A replay-storage failure leaves the site enrolled for a later retry,
and another run removes analytics rows written by an already-buffered flush.
`settings.erased_at` records a completed pass only: it is not a terminal state,
a resurrection-proof guarantee, or a distributed erasure acknowledgement.
