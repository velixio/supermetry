# Marketing site plan: landing, cloud pricing, docs, blog

Status: **proposed** (2026-10-11). Plan only, no code yet.
Stack is fixed by D26: static **Astro + Starlight**, served from a CDN. No Node server, no full-stack framework.
References: umami.is/pricing (quota baseline), openpanel.dev (layout and tone). Take ideas only, never copy their text, images or code (CLAUDE.md rule 3; OpenPanel is AGPL).

## 1. Site map

| Path | Page | Notes |
|---|---|---|
| `/` | Landing | Hero, live demo, features, OSS, pricing teaser, FAQ |
| `/pricing` | Cloud pricing | Plan cards, event slider, comparison table, FAQ |
| `/features` + `/features/<x>` | Feature pages | One page per pillar (web analytics, product analytics, heatmaps & replay, realtime, API & MCP, privacy) |
| `/self-host` | Self-hosting | One binary + Postgres, `docker compose up`, hardware needs, "every feature is free" (D12) |
| `/open-source` | OSS | Apache-2.0, GitHub stars, contributors, roadmap link, how to contribute |
| `/compare/<rival>` | Comparisons | vs Plausible, Umami, OpenPanel, Google Analytics, Fathom, Matomo, Mixpanel |
| `/migrate/<from>` | Migration guides | Plausible and Umami importers exist; GA4 later |
| `/demo` | Live demo | Public shared dashboard of supermetry.com's own traffic |
| `/docs/...` | Docs (Starlight) | See section 5 |
| `/blog/...` | Blog | Astro content collection, RSS, tags, authors |
| `/changelog` | Changelog | Generated from GitHub releases at build time |
| `/roadmap` | Public roadmap | From `roadmap.md` or GitHub milestones |
| `/benchmarks` | Benchmarks | Speed and RAM vs Plausible CE, Umami, OpenPanel (only after a VPS re-run, D15) |
| `/privacy`, `/terms`, `/dpa`, `/subprocessors`, `/security`, `/imprint` | Legal | Needed before cloud sign-up opens |
| `/about`, `/contact` | Company | Solo maintainer story, open about funding (D13) |
| `/status` | Status page | Link to an external status service |

App lives at `app.supermetry.com` (the cloud product, private repo, roadmap item 4). The marketing site only links to sign-up and log-in.

## 2. Landing page sections (in order)

1. **Nav**: logo, Features, Pricing, Docs, Blog, GitHub button **with live star count**, Log in, "Start free".
2. **Hero**: one line on the value ("Fast, privacy-first web and product analytics: one Go binary and Postgres"), two buttons: *Start free on Cloud* and *Self-host for free*. Product screenshot in light and dark (OpenPanel does this well).
3. **Social proof strip**: GitHub stars, Docker pulls, sites tracked. Show counts only once they are worth showing.
4. **Live product preview**: an embedded, animated dashboard mock (visitors, live "N online now", top sources), with a link to the real `/demo`.
5. **Feature grid** (6–9 cards): web analytics, custom events and goals, funnels, retention, user journeys, heatmaps, realtime, Web Vitals, alerts (Slack, Discord, Telegram, webhooks), Stats API + MCP server, importers, revenue goals.
6. **Why it's different** (our real edges, with numbers once verified):
   - No ClickHouse: runs on a $5 VPS, ~90 MB idle RAM, starts in 160 ms.
   - 1.9–5.6× faster than Plausible CE on 90-day filters (D25; re-run before publishing).
   - Ad-blocker resilient: custom script name and endpoint (D23).
   - Exact counts, no sampling (D21). VPN visitors counted (D39).
   - Cookie-free, no consent banner needed, raw IPs never stored.
7. **Privacy block**: GDPR/CCPA/PECR, EU hosting (decide), data ownership and export.
8. **Open source block**: Apache-2.0, "the same code runs our Cloud", star count, contributor avatars, "every feature is free to self-host" (D12).
9. **Cloud vs self-host** side by side.
10. **Pricing teaser**: three cards, link to `/pricing`.
11. **Testimonials**: leave out until we have real ones. Never invent quotes.
12. **FAQ** (8–10 questions).
13. **Final CTA** + footer: product, docs, compare, company, legal links; GitHub, Discord, X/Bluesky, Mastodon; newsletter sign-up.

## 3. Cloud pricing: Umami's quotas, doubled

Umami Cloud today (umami.is/pricing, fetched 2026-10-11): Hobby $0 (100K events, 1 site, 6 months), Pro $20 (1M, 20 sites, 10 members, 2 years, +$0.00003/event), Business $200 (10M, unlimited, 5 years, 5K replays, +$0.00002/event), Enterprise custom.

Proposed Supermetry Cloud, at the same prices with 2× on every quota:

| | Free | Pro | Business | Enterprise |
|---|---|---|---|---|
| Price/month | $0 | $20 | $200 | Custom |
| Events/month | **200K** | **2M** | **20M** | Custom |
| Extra events | n/a (soft cap, notify) | $0.000015 | $0.00001 | Custom |
| Websites | **2** | **40** | Unlimited | Unlimited |
| Team members | **2** | **20** | Unlimited | Unlimited |
| Data retention | **12 months** | **4 years** | **10 years** | Custom |
| Session replays | — | **1,000** | **10,000** | Custom |
| Extra replays | — | $0.005 | $0.0025 | Custom |
| Features | **All** | **All** | **All** | All + SSO, SLA, invoicing |
| Support | Community | Email | Email + chat | Dedicated |
| Free trial | — | 14 days, no card | 14 days, no card | — |

Rules to put on the page:
- **No feature gating.** Umami keeps heatmaps, replays, API and MCP for paid plans; we give every plan every feature and differ only by volume and support. This matches D12 and is our clearest pricing edge. (Decision needed: confirm.)
- Over quota: never drop data. Free plans get an email and then a soft limit; paid plans pay the per-event rate.
- Annual billing: 2 months free.
- Discounts: OSS projects and non-profits free Pro; students free.
- Event slider (OpenPanel style) that shows the price for any volume, plus a "you'd pay X at Umami / Plausible" line. Keep competitor prices dated and sourced.

Cost check (inference, needs validating): about 88 B/event after the raw window (D17) means 20M events ≈ 1.8 GB/month of Postgres, so doubling Umami's quotas looks affordable. Long retention is cheap because rollups and closed-tier rows are kept anyway. Raw events stay on the 30-day window; the page must say "retention" means reports, not raw rows.

## 4. GitHub stars and OSS signals

- Build time: read the star count, latest release and contributor list from the GitHub API and bake them into the HTML.
- Run time: a small client script refreshes the star count from `api.github.com` and keeps the baked number if the call fails or hits the rate limit.
- Star button in the nav and the OSS block; "Star us" prompt in docs and blog footers.
- Blocked until the repo is public (it is private now).
- Optional later: a public "open startup" page with stars, MRR and sites over time, served from our own Supermetry stats.

## 5. Docs (Starlight, at `/docs`)

- **Getting started**: Cloud in 2 minutes; self-host with Docker, binary or systemd.
- **Tracking**: script install, frameworks (Next, Nuxt, Astro, SvelteKit, WordPress, Shopify, Webflow, GTM), custom events and props, goals, revenue, outbound/download/404 tracking, `identify`, SPA routing, proxying past ad blockers.
- **Dashboard guides**: each report (funnels, retention, journeys, heatmaps, replay, realtime, Web Vitals, segments, annotations).
- **Self-hosting**: configuration (move in from `docs/guide/configuration.md`), GeoIP, backups, upgrades, reverse proxies, scaling, Redis (optional).
- **API**: reference generated from `internal/api/openapi.yaml`, authentication (`docs/api-auth.md`), Stats API, events API, webhooks, MCP server.
- **Migrating**: from Plausible, Umami, GA4.
- **Privacy and compliance**: from `docs/tracking-privacy.md`, plus a GDPR guide and DPA.
- Extras: `llms.txt` and per-page Markdown for AI tools, Pagefind search (Starlight default), "Edit on GitHub" links, versioned docs once v1 ships.

## 6. Blog

Astro content collection with RSS, tags, author pages, OG images built at build time, reading time, and a newsletter box. Launch with 5–6 posts:

1. Introducing Supermetry (why, what's different).
2. How we made analytics fast on plain Postgres without ClickHouse (three-tier storage, D21).
3. Benchmarks: Supermetry vs Plausible CE vs Umami vs OpenPanel (after the VPS re-run).
4. Cookie-free analytics and GDPR, explained.
5. Moving from Google Analytics / Plausible / Umami in 5 minutes.
6. Running analytics on a $5 VPS.

Then a steady cadence: release notes, engineering deep dives, privacy explainers, SEO guides.

## 7. More ideas (beyond Umami and OpenPanel)

- **Eat our own cooking**: track the site with Supermetry and make `/demo` its public dashboard.
- **Free SEO tools** that bring traffic: UTM builder, GA4-to-Supermetry cost calculator, "does my site need a cookie banner?" checker, tracking-script size comparison.
- **Glossary pages** (bounce rate, sessions, UTM, funnels): cheap, long-lived search traffic.
- **Integrations directory**: one page per framework or platform, linking to its docs page.
- **"One binary" install demo**: an asciinema clip of `docker compose up` to the first pageview in under 60 s.
- **Hardware calculator** on `/self-host`: enter monthly pageviews and get the CPU, RAM and disk you need.
- **Badge**: "Privacy-friendly analytics by Supermetry" for users' footers (backlinks).
- **Community**: Discord or GitHub Discussions, a public roadmap with voting, a "good first issue" page.
- **Trust pages**: security page (threat model from `docs/security/threat-model.md`), subprocessors, EU data residency, uptime history.
- **Launch plan**: Show HN, Product Hunt, r/selfhosted, awesome-selfhosted listing, AlternativeTo, a Hetzner/Coolify/CapRover/Railway one-click template.
- **Affiliate or referral program** later (Plausible-style 50% for the first year).
- **i18n** of the landing page later (German and French first, given the GDPR market).

## 8. Open decisions (for the user)

1. ~~No feature gating on Cloud~~: accepted (D64).
2. ~~14-day trial~~: accepted (D64). Prices, quotas and overage rates as in section 3 unless the user says otherwise.
3. ~~EU hosting only~~: accepted (D64). Legal entity: **Yupcha** (D65).
4. ~~Domain layout~~: accepted (D64): `supermetry.com` (site + `/docs` + `/blog`), `app.supermetry.com` (cloud).
5. ~~Making the repo public~~: the user does it themselves.
6. ~~Trademark search~~: deferred by the user (D65).
7. Which benchmark numbers we may publish, and when (needs the VPS re-run, D15/D25).
8. Hosting: a CDN is not required. Recommended start: the static files served by Caddy on a small EU server, separate from the cloud app, with an EU CDN (e.g. Bunny) added only if global traffic needs it.

## 9. Build order (once approved)

1. Install the `astrolicious/agent-skills@astro` skill; scaffold `site/` (Astro + Starlight + Tailwind, Bun, D26), sharing brand tokens and logo from `web/public/brand/`.
2. Landing page and `/pricing` (with slider and comparison table).
3. Docs: move the existing `docs/` guides in, generate the API reference from OpenAPI.
4. Blog with RSS and OG images, plus the first two posts.
5. Feature, compare, migrate, self-host and OSS pages.
6. Legal pages, then `/demo` once the site is tracked.
7. Build-time GitHub data and the client star refresh.
8. Lighthouse 100 / zero JS by default as the acceptance bar; deploy to the CDN.
