# Features

Panel compared with Plausible, Umami and OpenPanel, feature by feature.
✓ has it · – lacks it · ? not verified yet (check `knowledge/` before relying on it).
**Panel**: ✅ done · ⏳ in progress · ❌ not started · ⏸ deferred.
Evidence for the other products: [findings.md](findings.md). Last updated 2026-10-08.

## Analytics

| Feature | Plausible | Umami | OpenPanel | Panel |
|---|---|---|---|---|
| Visitors, pageviews, bounce rate, visit duration | ✓ | ✓ | ✓ | ✅ |
| Realtime view | ✓ | ✓ | ✓ | ✅ |
| Compare with the previous period | ✓ | ✓ | ? | ✅ |
| Filters on any dimension | ✓ | ✓ | ✓ | ✅ |
| Page, source, country, browser, OS and device breakdowns | ✓ | ✓ | ✓ | ✅ |
| UTM breakdowns | ✓ | ✓ | ✓ | ✅ |
| Entry and exit pages | ✓ | ✓ | ✓ | ✅ |
| Region and city | ✓ | ✓ | ✓ | ✅ |
| Custom events and props | ✓ | ✓ | ✓ | ✅ |
| Goals | ✓ | ✓ | ? | ✅ |
| Funnels | ✓ (not in CE) | ✓ | ✓ | ✅ |
| Revenue goals | ✓ (paid) | ✓ | ✓ | ✅ |
| Saved segments | ✓ | ✓ | ✓ | ✅ |
| Annotations on the chart | – | ✓ | – | ✅ |
| Retention (cohort) report | – | ✓ | ✓ | ✅ (same-day only) |
| User journeys / path flow | ✓ (paid) | ✓ | ✓ | ✅ |
| Web Vitals | – | ✓ | ? | ✅ (full script) |
| Session replay | – | ✓ | ✓ | ⏸ |
| Heatmaps | – | ✓ | – | ⏸ |
| User profiles and `identify` | – | ✓ | ✓ | ⏳ (opt-in per site) |
| AI insights | – | ? | ✓ | ⏸ |

## Tracking

| Feature | Plausible | Umami | OpenPanel | Panel |
|---|---|---|---|---|
| Script under 1 KB gzip | ✓ | – | – | ✅ (929 B) |
| No cookies | ✓ | ✓ | ✓ | ✅ |
| SPA pageviews | ✓ | ✓ | ✓ | ✅ |
| Scroll depth and engagement time | ✓ | – | ? | ✅ |
| Automatic outbound-link, file-download and 404 tracking | ✓ | ✓ | ✓ | ✅ (full script, 1.5 KB gz) |
| Click tracking from HTML attributes | ✓ | ✓ | ✓ | ✅ (`data-panel-event`) |
| Server-side events API, with examples | ✓ | ✓ | ✓ | ❌ (API exists, no docs) |
| Bot filtering; IP and path exclusion | ✓ | ✓ | ✓ | ✅ |
| Mobile SDKs | – | – | ✓ | ⏸ |

## Team, sharing and API

| Feature | Plausible | Umami | OpenPanel | Panel |
|---|---|---|---|---|
| Teams / orgs with roles | ✓ | ✓ | ✓ | ✅ |
| Per-site access | ✓ | ✓ | ? | ✅ |
| Two-factor auth (TOTP) | ✓ | ✓ | ? | ✅ |
| Audit log | ? | – | – | ✅ |
| Public share links | ✓ | ✓ | ✓ | ✅ |
| Email reports | ✓ | – | ? | ✅ |
| Alerts: traffic spike or drop, goal hit | ✓ (email, traffic spike) | – | ✓ | ✅ |
| Webhooks, Slack, Discord | – | – | ✓ | ✅ |
| CSV export | ✓ | ✓ | ? | ✅ (every report, streamed) |
| Full export; import from Plausible, Umami, GA4 | ✓ | partial | ✓ | ✅ (GA4 import not yet) |
| Stats API: summary | ✓ | ✓ | ✓ | ✅ |
| Stats API: timeseries, breakdowns, realtime | ✓ | ✓ | ✓ | ✅ |
| Sites / goals / funnels API | ✓ (not in CE) | ✓ | ✓ | ✅ |
| MCP server for AI assistants | ? | ✓ | ✓ | ✅ |
| SSO / OIDC | ✓ (enterprise) | ? | ? | ⏸ |

## Self-hosting

| Feature | Plausible | Umami | OpenPanel | Panel |
|---|---|---|---|---|
| No ClickHouse | – | ✓ | – | ✅ |
| Single binary | – | – | – | ✅ |
| Database | PG + ClickHouse | PG / MySQL | PG + ClickHouse + Redis | PG only |
| Minimum RAM | ~2 GB | ~150 MB | 2 GB | ⏳ (~90 MB idle, target <50 MB) |
| Data retention / purge | ? | – | ? | ✅ (per site, partition drop) |
| Docker image | ✓ | ✓ | ✓ | ❌ |
| Release binaries, systemd unit, install docs | – | – | – | ❌ |
| License | AGPL-3.0 | MIT | AGPL-3.0 | ❌ (Apache-2.0 decided, LICENSE file missing) |

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
11. ~~Retention report~~ ✅ (same-day return only: visitor IDs rotate daily by design)
12. ~~Alerts: traffic spike or drop, goal hit~~ ✅ (spike window is the whole local day for now)
13. ~~Annotations on the chart~~ ✅
14. ~~Importers from Plausible and Umami~~ ✅ (GA4 not yet)
15. ~~Webhooks, Slack and Discord~~ ✅
16. ~~MCP server for AI assistants~~ ✅
17. ~~Web Vitals~~ ✅
18. User profiles and `identify` (opt-in per site, D40) ⏳

### Later

- LICENSE file (Apache-2.0)
- Docker image, release binaries, systemd unit, install docs
- Server-side events API docs; landing page and docs (Astro + Starlight)
- Security review
- Optimisation: ~~startup time~~ ✅ (9.8 s → 160 ms), idle RAM (~90 MB now, target <50 MB), product benchmark (D25)

Deferred: session replay, heatmaps, AI insights, mobile SDKs, SSO/OIDC.
