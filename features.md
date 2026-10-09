# Features

Supermetry compared with Plausible, Umami and OpenPanel, feature by feature.
✓ has it · – lacks it · ? not verified yet (check `knowledge/` before relying on it).
**Supermetry**: ✅ done · ⏳ in progress · ❌ not started · ⏸ deferred.
Evidence for the other products: [findings.md](findings.md). Last updated 2026-10-08.

## Analytics

| Feature | Plausible | Umami | OpenPanel | Supermetry |
|---|---|---|---|---|
| Visitors, pageviews, bounce rate, visit duration | ✓ | ✓ | ✓ | ✅ |
| Realtime view | ✓ | ✓ | ✓ | ✅ (live feed, per-minute chart) |
| Compare with the previous period | ✓ | ✓ | ? | ✅ (previous period, previous year) |
| Periods incl. 28d, 91d, 6mo | ✓ | ✓ | ? | ✅ |
| Time on page and exit rate | ✓ | – | ? | ✅ |
| Filters on any dimension | ✓ | ✓ | ✓ | ✅ (Add-filter picker with value suggestions) |
| Sessions explorer and session timeline | – | ✓ | ✓ | ✅ |
| Events and properties explorer | – | ✓ | ✓ | ✅ |
| Page, source, country, browser, OS and device breakdowns | ✓ | ✓ | ✓ | ✅ |
| UTM breakdowns | ✓ | ✓ | ✓ | ✅ (raw `utm_source` stored separately from the blended source) |
| Entry and exit pages | ✓ | ✓ | ✓ | ✅ |
| Region and city | ✓ | ✓ | ✓ | ✅ (world map on the Countries card) |
| Custom events and props | ✓ | ✓ | ✓ | ✅ |
| Goals | ✓ | ✓ | ? | ✅ |
| Funnels | ✓ (not in CE) | ✓ | ✓ | ✅ |
| Revenue goals | ✓ (paid) | ✓ | ✓ | ✅ |
| Saved segments | ✓ | ✓ | ✓ | ✅ |
| Annotations on the chart | – | ✓ | – | ✅ |
| Retention (cohort) report | – | ✓ | ✓ | ✅ (cross-day with profiles; same-day for anonymous) |
| User journeys / path flow | ✓ (paid) | ✓ | ✓ | ✅ |
| Web Vitals | – | ✓ | ? | ✅ (full script) |
| Session replay | – | ✓ | ✓ | ✓ |
| Heatmaps | – | ✓ | – | ✅ (aggregated clicks + scroll map, opt-in) |
| User profiles and `identify` | – | ✓ | ✓ | ✅ (opt-in per site) |
| AI insights | – | ? | ✓ | ⏸ |

## Tracking

| Feature | Plausible | Umami | OpenPanel | Supermetry |
|---|---|---|---|---|
| Script under 1 KB gzip | ✓ | – | – | ✅ (936 B core; full script 2.3 KB) |
| No cookies | ✓ | ✓ | ✓ | ✅ |
| SPA pageviews | ✓ | ✓ | ✓ | ✅ |
| Scroll depth and engagement time | ✓ | – | ? | ✅ |
| Automatic outbound-link, file-download and 404 tracking | ✓ | ✓ | ✓ | ✅ (full script, 1.5 KB gz) |
| Click tracking from HTML attributes | ✓ | ✓ | ✓ | ✅ (`data-supermetry-event`) |
| Server-side events API, with examples | ✓ | ✓ | ✓ | ✅ (`docs/api.md`) |
| Bot filtering; IP, path and country exclusion | ✓ | ✓ | ✓ | ✅ |
| Per-site tracker settings (served script) | ✓ | – | – | ✅ (outbound, downloads, forms, 404, hash routing, clicks, Web Vitals, heatmaps) |
| Hash-based routing, form submissions | ✓ | ✓ | ✓ | ✅ (per-site setting) |
| Do Not Track / GPC, visitor opt-out | ✓ (opt-out) | ✓ (DNT) | – | ✅ (respected by default; `supermetry.optOut()`) |
| Consent mode, manual pageviews, `beforeSend` hook | – | ✓ | ✓ (consent) | ✅ |
| npm tracker package | ✓ | – | ✓ | ✅ |
| Mobile SDKs | – | – | ✓ | ⏸ |

## Team, sharing and API

| Feature | Plausible | Umami | OpenPanel | Supermetry |
|---|---|---|---|---|
| Teams / orgs with roles | ✓ | ✓ | ✓ | ✅ |
| Per-site access | ✓ | ✓ | ? | ✅ |
| Two-factor auth (TOTP) | ✓ | ✓ | ? | ✅ |
| Account self-service: edit name, change email or password, delete account | ✓ | ✓ | ✓ | ✅ |
| Invite-only signup by default | ✓ (`DISABLE_REGISTRATION`) | – | – | ✅ (`SUPERMETRY_SIGNUP=open` to allow) |
| Audit log | ? | – | – | ✅ |
| Public share links | ✓ | ✓ | ✓ | ✅ |
| Email reports | ✓ | – | ? | ✅ |
| Alerts: traffic spike or drop, goal hit | ✓ (email, traffic spike) | – | ✓ | ✅ |
| Webhooks, Slack, Discord | – | – | ✓ | ✅ |
| CSV export | ✓ | ✓ | ? | ✅ (every report, streamed) |
| Full export; import from Plausible, Umami, GA4 | ✓ | partial | ✓ | ✅ (GA4 import not yet) |
| Stats API: summary | ✓ | ✓ | ✓ | ✅ |
| Stats API: timeseries, breakdowns, realtime | ✓ | ✓ | ✓ | ✅ |
| API: sessions, raw events, properties, suggestions | – | ✓ | ✓ | ✅ |
| API key management through the API, scoped keys | ✓ (scopes) | ✓ | – | ✅ (`keys:manage`, no escalation) |
| API reference in the app + typed client | ✓ (docs) | ✓ (client) | ✓ | ✅ |
| Sites / goals / funnels API | ✓ (not in CE) | ✓ | ✓ | ✅ |
| MCP server for AI assistants | ? | ✓ | ✓ | ✅ |
| SSO / OIDC | ✓ (enterprise) | ? | ? | ⏸ |

## Self-hosting

| Feature | Plausible | Umami | OpenPanel | Supermetry |
|---|---|---|---|---|
| No ClickHouse | – | ✓ | – | ✅ |
| Single binary | – | – | – | ✅ |
| Database | PG + ClickHouse | PG / MySQL | PG + ClickHouse + Redis | PG only |
| Minimum RAM | ~2 GB | ~150 MB | 2 GB | ⏳ (~90 MB idle, target <50 MB) |
| Protected metrics endpoint | ✓ | ? | ? | ✅ (`SUPERMETRY_METRICS_TOKEN` or localhost only) |
| Data retention / purge | ? | – | ? | ✅ (per site, partition drop) |
| Automatic rollup backfill after upgrades | – | – | – | ✅ (progress on Data health) |
| Docker image | ✓ | ✓ | ✓ | ✅ (`Dockerfile`, `deploy/docker-compose.yml`) |
| Release binaries, systemd unit, install docs | – | – | – | ✅ (`deploy/install.sh`, `deploy/supermetry.service`, `deploy/README.md`) |
| Backup and restore command | – | – | – | ✅ (`supermetry backup`, `supermetry restore`) |
| License | AGPL-3.0 | MIT | AGPL-3.0 | ✅ (Apache-2.0, `LICENSE`) |

## Priority

Most used features first: what people look at every day, then what they configure, then integrations.
Hosting, docs, Docker, security and optimisation come later (user, 2026-10-08).

### Build now (most used)

1. ~~Entry and exit pages~~ ✅ (already built)
2. ~~Region and city breakdowns~~ ✅
3. ~~Automatic outbound-link, file-download and 404 tracking~~ ✅
4. ~~Click tracking from HTML attributes~~ ✅
5. ~~Site exclusion rules: IP ranges, paths~~ ✅
6. ~~Saved segments~~ ✅
7. ~~Revenue goals~~ ✅
8. ~~Stats API: timeseries, breakdowns, realtime~~ ✅
9. ~~Full CSV export of every report~~ ✅
10. ~~User journeys / path flow~~ ✅
11. ~~Retention report~~ ✅ (cross-day for identified profiles; same-day for anonymous visitors)
12. ~~Alerts: traffic spike or drop, goal hit~~ ✅ (spike window is the whole local day for now)
13. ~~Annotations on the chart~~ ✅
14. ~~Importers from Plausible and Umami~~ ✅ (GA4 not yet)
15. ~~Webhooks, Slack and Discord~~ ✅
16. ~~MCP server for AI assistants~~ ✅
17. ~~Web Vitals~~ ✅
18. ~~User profiles and `identify`~~ ✅ (opt-in per site, D40)
19. ~~Heatmaps: aggregated click grid and scroll map~~ ✅ (opt-in per site)
20. ~~Richer realtime (live feed, per-minute chart, all breakdowns) and SVG country flags~~ ✅

### Later

- ~~LICENSE file~~ ✅, ~~Docker image, release binaries, systemd unit, installer~~ ✅, ~~backup/restore~~ ✅
- ~~Server-side events API docs~~ ✅ (`docs/api.md`); landing page and docs (Astro + Starlight)
- ~~Trust fixes T1–T8~~ ✅ (see competitor-profiles/_summary.md); full security review still to do
- ~~Accuracy check (reference, 0 mismatches)~~ ✅; real-traffic comparison with Plausible (`bench/accuracy/compare-live.py`) and product benchmark (D25) ⏳
- Optimisation: ~~startup time~~ ✅ (9.8 s → 160 ms), idle RAM (~90 MB now, target <50 MB), product benchmark (D25)

Full gap analysis against every reference repo: [competitor-profiles/_summary.md](competitor-profiles/_summary.md).

Deferred: AI insights, mobile SDKs, SSO/OIDC.
