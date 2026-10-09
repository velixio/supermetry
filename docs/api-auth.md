# API authentication

Supermetry's integration API lives under `/api/public/v1`. "Public" means *for integrations*: every request needs an API key. The dashboard uses a separate, unversioned API under `/api/private` with a login session; don't call that one from scripts.

## Create a key

1. In the dashboard, open **Organization settings → API keys** and click **Create key**.
2. Give it a name, choose its **scopes**, and optionally limit it to **some sites** and set an **expiry date**.
3. Copy the key. It is shown **once**; Supermetry stores only a hash.

Keys belong to an organization, not to a person, so they keep working when the person who created them leaves. Every key shows when it was last used, and you can revoke it at any time.

## Send the key

Pass it in the `Authorization` header with the Bearer scheme:

```http
Authorization: Bearer <api-key>
```

```sh
curl https://supermetry.com/api/public/v1/sites \
  -H "Accept: application/json" \
  -H "Authorization: Bearer <api-key>"
```

## Scopes

A key can do only what its scopes allow.

| Scope | Allows |
|---|---|
| `stats:read` | Aggregate analytics, stats, queries, realtime, funnels, journeys, retention, heatmaps, Web Vitals, sessions/events without profile identity or replay metadata, properties, suggestions, and ordinary resource listings. Also the MCP server, with the same resource restrictions. |
| `profiles:read` | Read profiles and profile identity fields within allowed sites |
| `replay:read` | Read replay lists, recordings and replay metadata within allowed sites |
| `data:export` | Download analytics CSV/ZIP, in addition to the relevant read scope |
| `sites:write` | Update/delete allowed sites; creating a site requires an unrestricted organization key |
| `goals:write` | Create, update and delete goals and funnels |
| `annotations:write` | Create, update and delete chart annotations |
| `segments:write` | Create, update and delete saved segments |
| `keys:manage` | List, create and revoke keys within the caller's authority (see below). Only an owner or admin can grant it. |

Give each integration the smallest set it needs. A JSON analytics script needs only `stats:read`; CSV/ZIP export also needs `data:export`. Profile or recording access requires its own explicit scope.

## Site restriction and expiry

- **Sites:** a key limited to some sites behaves as if the other sites don't exist. Requests for them return **404**, so the key can't even confirm they exist.
- **Expiry:** an expired key is rejected like a revoked one.

## Rotating keys through the API

A key with `keys:manage` can manage keys, so a deploy script can rotate its own credentials:

- `GET /api/public/v1/api-keys` lists keys within the caller's scope/site authority (name, scopes, sites, created, expires, last used, revoked). It never returns secrets.
- `POST /api/public/v1/api-keys` creates a key and returns its secret once.
- `DELETE /api/public/v1/api-keys/{key}` revokes a key. A key may revoke itself.

A key can never create a key that can do more than itself:
- the new key's scopes must be a subset of the caller's scopes;
- its sites must be a subset of the caller's sites;
- its expiry can't be later than the caller's;
- a key cannot delegate `keys:manage`, even if it has that scope.

Restricted key managers cannot list or revoke broader keys. Human issuers are revalidated against their live membership; every supplied site must belong to the key's organization.

Anything broader is rejected with **403**. Every create and revoke is recorded in the audit log.

## Authorization foundation upgrade

Existing role names and keys remain valid, but `stats:read` no longer implicitly permits profiles, recordings or exports. Existing keys are **not** backfilled with sensitive scopes. Historical persisted keys with unknown or wildcard scopes fail closed; the separately configured instance administrator key keeps its existing explicit authority. Create a replacement with only the explicit scopes the integration needs, then revoke the old key. Editor/Viewer memberships similarly need explicit profile/replay/export grants; Owner/Admin presets include them. Shared dashboards stay aggregate-only and cannot export.

Site restrictions are precise: `site_ids: null` means all sites in the organization, and `site_ids: []` means no sites. Billing Admin is a separate member assignment and never grants integration or analytics access.

## Errors

Errors use RFC 9457 `application/problem+json`.

| Status | Meaning |
|---|---|
| 401 | Missing, malformed, revoked or expired key |
| 403 | The key is valid but lacks the scope this endpoint needs |
| 404 | The site, or object, doesn't exist **or** the key may not see it |
| 429 | Rate limit reached; wait for the time in `Retry-After` |

## Rate limits

Each key gets a steady 2 requests per second with bursts of up to 30. Responses carry `RateLimit-Limit`, `RateLimit-Remaining` and `RateLimit-Reset` headers. Use the `cursor` on paginated endpoints rather than many small requests.

## Why there's no username and password login for the API

Some tools let scripts trade a username and password for a token. Supermetry doesn't. A script holding a real password can do everything that person can, including changing their password, and it breaks when two-factor login is on. Scoped, site-limited, expiring keys give a script exactly the access it needs and can be revoked without touching anyone's account.
