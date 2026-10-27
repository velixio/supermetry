# Per-organization replay retention cap: design

Date: 2026-10-27. Status: accepted.

## Intent

A hosted deployment gives each plan its own replay retention (free 30 days, paid 45). The hosting layer pushes a number per organization; core stores and enforces it.

## Model

- `orgs.replay_retention_cap_days integer null`, check 1..365 (migration 0250). Null means no org cap, which is every existing organization, so nothing changes until a cap is set.
- Effective retention of a site = min(`SUPERMETRY_REPLAY_RETENTION_DAYS` when set, the org cap when set, the site's `retention_days`, default 30). One resolver, `replay.ResolveSite`, handles it; the cap travels in `sites.ReplaySiteInput.OrgRetentionCapDays` (not stored in the site JSON) and is loaded by the site registry, the settings handlers and the cleanup job from `orgs`.
- Reporting: when the org cap lowers the value, the value's `restriction` is `org_retention_capped` and the top-level `restrictions` list carries it; `maximum` is the smaller of the instance and org ceilings. The instance cap keeps `instance_retention_days_cap` when it is the one lowering the value (ties report the instance). A site cannot save a retention above the org cap (409), like the instance cap.

## Admin API

`GET|PUT|DELETE /api/private/admin/organizations/{org}/replay-retention-cap`, instance admins only. `PUT {"days": N|null}`; `DELETE` = null; answers `{"days": N|null, "source": "org"|"none"}`. Audited as `replay.retention_cap_changed` (metadata: days). A change refreshes the site registry so ingest sees it at once.

## Cleanup semantics

The hourly retention job uses the effective value, so lowering a cap deletes older recordings at the next run: that is what retention means, and raising the cap later never resurrects them. Each run logs one line per site (`replay retention`: site id, effective days, org cap, sessions deleted for age, sessions kept), counts only; sites with neither deletions nor a cap log at debug level.

## Tests

Resolver table (instance only, org only, both, project lower, null everywhere identical to before); cleanup with two organizations on identical site settings; handler matrix and audit row; settings API; migration; web restriction display.
