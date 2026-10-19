# Supermetry threat model

Status: first version, 2026-10-10, written against `main` at `cf449dc`. All file references are relative to the repository root. Line numbers drift, so re-check them before relying on one.

This document covers the public and semi-public surfaces of one Supermetry instance: a Go binary, PostgreSQL, and the embedded dashboard. For how to respond to abuse, see the operator runbook in [SECURITY.md](../../.github/SECURITY.md#abuse-runbook).

How to read the tables:

- **Control** means it exists in code today, with the file that implements it.
- **Missing** means a control a reader would reasonably expect, which we looked for and did not find.
- **Planned** marks a next step that does not exist yet. **Done** marks a control added after the first version of this analysis.
- Anything we could not confirm by reading code is marked *unverified*.

## Actors

| Actor | Reaches | Notes |
|---|---|---|
| Anonymous internet | Ingest, tracker script, replay intake, login, signup (if open), password reset, share links, `/healthz`, `/readyz` | Includes bots, spammers, credential stuffers |
| Site visitor | Same as anonymous; the tracker runs in their browser | Their data is what we store |
| Share viewer | One site's summary stats through `/api/private/share/{slug}/...` | Holds the share slug, and the password if one is set |
| API key holder | `/api/public/v1/*` and `/api/public/v1/mcp`, limited by the key's scopes and sites | May be a script, CI job or LLM agent |
| Org member (viewer, editor) | Dashboard and private API for assigned sites | |
| Org owner/admin | Site settings, exclusions, keys, shares, imports, exports, alerts | |
| Instance operator | Env vars, CLI (`backup`, `restore`, `user`, `migrate-labels`), database, disk | Fully trusted |

## Trust boundaries and assets

| Asset | Where | Why it matters |
|---|---|---|
| Event, session and rollup data | Postgres (`events`, `sessions`, rollups) | Integrity of every report; may contain PII from URLs, props or profiles |
| Session replays and CSS | `SUPERMETRY_REPLAY_DIR` / `$SUPERMETRY_DATA_DIR/replay`, or S3 | Can contain page text and personal data |
| Account credentials | `users.password_hash` (Argon2id), `user_sessions` (SHA-256 of the token) | Account takeover |
| TOTP secrets, alert channel URLs and tokens, replay S3 secret | Postgres, sealed with `SUPERMETRY_SECRET_KEY` | Second factor; outbound credentials |
| API keys, share slugs, invite, reset and setup tokens | Postgres, SHA-256 hashes only | Bearer access |
| Daily visitor salts | Postgres `salts`, plaintext, deleted after about 48 h | With the IP and UA they re-identify visitors for that day |
| `SUPERMETRY_SECRET_KEY`, `SUPERMETRY_ADMIN_API_KEY`, `SUPERMETRY_METRICS_TOKEN` | Environment | See surface 6 |
| Disk and DB capacity | Host | Exhausting them is denial of service |

---

## 1. Event ingest and tracker script

**Surface.**

- `POST /api/public/v1/events`: set by `SUPERMETRY_EVENT_PATH`, mounted with no auth at `internal/api/router.go:154-156`.
- `GET /js/script.js`, the `.full.js` variant, and `GET /js/<publicID>.js`: set by `SUPERMETRY_TRACKER_PATH`, at `internal/api/router.go:167-173`.

**Who can reach it.** Anyone. The site's public ID is embedded in the snippet on every tracked page, so it is not a secret.

### Controls in place

| Control | Reference |
|---|---|
| Body capped at 8 KiB and must be valid UTF-8; props, URL and name lengths are capped | `internal/ingest/handler.go:87`, `internal/ingest/payload.go:17-30,75` |
| Site resolved from the body's `site` field through an in-memory registry (no DB read) | `internal/ingest/handler.go:106` |
| Page URL host must match the site's `allowed_hosts` (exact, or `*.example.com` suffix). An empty list rejects everything | `internal/ingest/handler.go:145-149`, `internal/sites/registry.go:40-57` |
| Token-bucket rate limits: 1,000/s per IP (burst 2,000), 5,000/s per site (burst 10,000), 10,000/s per process. IPv6 is keyed by /64. Over the limit returns 429 | `cmd/supermetry/serve.go:318`, `internal/ingest/ratelimit.go:139-147`, `internal/ingest/handler.go:230-235,575-582` |
| Bounded write queue: when full, 503 and `ingest_dropped_total` | `internal/ingest/handler.go:629-633`, `SUPERMETRY_QUEUE_SIZE` |
| Dedup on `site + id` | `internal/ingest/handler.go:154-161`, `SUPERMETRY_DEDUP_ENTRIES` |
| Events more than 24 h late are dropped; events more than 5 min in the future are clamped | `internal/ingest/handler.go:163-174` |
| Bot filter: headless and crawler user agents, empty UA, and a referrer-spam domain list matched on the host and every parent domain (static, embedded at build time) | `internal/enrich/bot.go:31-32,47-75`, `internal/enrich/lists/` |
| Per-site exclusions by IP/CIDR, path and country, applied before storage | `internal/ingest/handler.go:194-229`, `internal/sites/exclusions.go` |
| Per-site, per-day cardinality cap: 10,000 distinct values per dimension (200 for versions, language, hostname); the rest become `Other` | `internal/ingest/handler.go:511-529`, `internal/ingest/valuecap.go` |
| Privacy signals (GPC/DNT) honoured when the site enables it | `internal/ingest/handler.go:123-127` |
| Raw IP is used only for the rate key, geo and the salted visitor hash, not persisted | `internal/ingest/handler.go:286`, `internal/clientip/clientip.go:1-3` |
| Counters on `/metrics` and the Data health page | `internal/health/health.go`, `internal/api/metrics.go` |

### Abuse cases

| Abuse | Feasible today? | Why |
|---|---|---|
| Spam events for someone else's site ID | **Yes** | Ingest checks only the `url` field inside the body, which the sender controls. `Origin` and `Referer` are never read (`internal/ingest/handler.go:145`). Any script that knows the public ID and an allowed host can post events |
| Referrer spam | Partly blocked | Only domains on the static list are filtered. New spam domains get through until the list is regenerated and a release ships |
| Flood from one IP | Limited | 1,000 events/s per IP is enough to badly skew a small site's numbers |
| Flood from many IPs / IPv6 rotation | Limited by the per-site and global buckets only | Beyond 10,000 tracked buckets, new keys share one overflow bucket (`internal/ingest/ratelimit.go:110-115`). A flood that saturates the global bucket starves every site on the instance |
| Dictionary exhaustion | **Yes, instance-wide** | The value dictionary is shared by all sites and capped at 100,000 entries per dimension kind (`internal/dict/dict.go:46`). One site's public ID can add about 10,000 new values per kind per day, and after the cap new values on every site become `Other`. Each new value is also a synchronous DB write on a cache miss (`internal/dict/dict.go:158-165`, as reported by the helper pass; not re-read) |
| Visitor collapse or rate-limit sharing behind an unconfigured proxy | **Yes, by misconfiguration** | See "Client IP" below |

### Client IP (`SUPERMETRY_TRUSTED_PROXIES`)

`clientip.Resolve` (`internal/clientip/clientip.go:15-35`) trusts `X-Forwarded-For` only when the TCP peer is inside `SUPERMETRY_TRUSTED_PROXIES`. It then walks right to left and takes the first public address that is not itself a proxy.

When the variable is empty, loopback and private networks (`127.0.0.0/8`, `::1`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `fc00::/7`) are trusted, so a proxy on the same host, LAN or Docker network works without setup (`internal/config/config.go`, `defaultTrustedProxies`). The trade-off: a client that reaches Supermetry directly from a private network can set its own `X-Forwarded-For`, spoofing its country and its key in the per-IP ingest and login limits. Operators with untrusted private clients set `none`.

If the server runs behind a proxy with a public address (Cloudflare, a cloud load balancer) that is not listed, or the variable is `none` behind any proxy:

- Every visitor gets the proxy's IP. All visitors then share one 1,000/s bucket, and visitor IDs (`H(salt, site, ip, UA)`) merge everyone with the same browser.
- Geo lookup and IP exclusions see the proxy address.
- `/metrics` treats requests as loopback when the proxy runs on the same host, so it is reachable from the internet through the proxy (`internal/api/metrics.go:23-24`) unless `SUPERMETRY_METRICS_TOKEN` is set.
- Nothing warns at startup. Only the count of trusted prefixes is logged.

A second, smaller issue: `Resolve` skips private hops, so a client whose real address is private (an intranet behind the proxy) can put a forged public address further left in `X-Forwarded-For`. Access decisions use `ResolveAccessClient`, which does not skip private hops.

### Residual risk and next steps

| Residual risk | Severity | Next step |
|---|---|---|
| Forged events for any site, from any client | High for data integrity | **Done:** opt-in per-site strict origin mode (requires a matching `Origin` or `Referer`; see `docs/tracking-privacy.md`). It stops browser-based spam from other origins, not scripted senders. Consider an optional signed or proxied event endpoint for high-value sites |
| Referrer spam outside the static list | Medium | **Done:** per-site blocked-referrer list and the operator file `SUPERMETRY_REFERRER_SPAM_FILE` (startup only) |
| Shared 100k-per-kind dictionary, so one site can degrade all sites | Medium | **Done:** per-site share of each kind's dictionary capacity, daily garbage collection of unreferenced entries, and 80% and 95% fill warnings. Overflow is also counted per site on the health page |
| Rate limits are hard-coded and per process | Medium | Make them configurable. Document that multi-instance deployments need a limiter in the proxy |
| No per-site view of drops | Medium | **Done:** per-site daily drop counts with spike warnings on the health page (bots by rule name, rate limits, privacy signals, overflow). Only counts are stored, never IPs or user agents |
| Public proxy missing from `SUPERMETRY_TRUSTED_PROXIES` | Medium (misconfiguration) | Log a startup warning when requests arrive with `X-Forwarded-For` from an untrusted peer |
| No tool to delete spam already stored, by time range, referrer or IP | Medium | Add a site-scoped purge for a time range plus filter, followed by a rollup rebuild. Today the options are profile erasure (identified mode only), retention, or deleting the site |
| Public ID cannot be rotated | Low | Add rotation, keeping the old ID accepted for a grace period |

---

## 2. Session replay intake and CSS upload

**Surface.** `POST /api/replay/start`, `POST /api/replay/chunk`, `PUT /api/replay/css/{hash}`, `GET /replay.js`. These are mounted only when `SUPERMETRY_REPLAY_ENABLED` is true (`internal/api/router.go:157-166,175-179`), and each site must also enable replay.

**Who can reach it.** Anyone who knows the site public ID and the current policy version, which the per-site tracker script exposes.

### Controls in place

| Control | Reference |
|---|---|
| Two-level opt-in: the instance flag and the per-site setting | `cmd/supermetry/serve.go:335`, `internal/replay/settings.go:91` (helper-reported) |
| Start body capped at 4 KiB; chunk at 256 KiB, which must be gzip | `internal/replay/handler.go:27-29,392-406` |
| Origin header must match `allowed_hosts` **if present**; the body URL must match too | `internal/replay/handler.go:190,462-469` |
| Separate limiter: 250/s, burst 500, per site + IP, shared with CSS | `cmd/supermetry/serve.go:336`, `internal/replay/handler.go:387-390` |
| 64 concurrent uploads; per-site byte budget of 64 MiB/min | `internal/replay/handler.go:31-35` |
| Disk guard: refuse writes below 2 GiB free | `internal/replay/diskguard.go:5` |
| CSS: SHA-256 must match the path; 1 MiB compressed, 4 MiB decompressed; per-site cap of 10k blobs or 512 MiB | `internal/replay/css_handler.go:21-26,130-210` |
| The server never fetches CSS URLs (no SSRF here) | `internal/replay/css_handler.go` (upload only) |
| Hourly retention and quota job (defaults: 30 days, 5 GiB) | `cmd/supermetry/serve.go:696`, `internal/replay/settings.go:18-20` |
| Dashboard playback goes through authenticated, site-scoped routes; the player iframe has no `allow-scripts`; the global CSP applies | `internal/platform/http/handlers_replay.go`, `cmd/supermetry/serve.go:512` |

### Abuse cases

| Abuse | Feasible today? | Why |
|---|---|---|
| Fill the disk with replay data | Partly bounded | Up to 64 MiB/min per site. Quota is enforced only by the hourly job, so a site can overshoot by up to about 3.75 GiB per hour. CSS bytes are outside the quota. The 2 GiB disk guard is the last stop, and it stops replay for every site |
| Forged replays from a non-browser client | **Yes** | An empty `Origin` is accepted (`internal/replay/handler.go:463-464`). A chunk needs a live analytics session, which a forged pageview creates |
| Hostile replay content shown to dashboard users | Low | Content is stored as is (no server-side validation). Scripts are blocked by the sandbox and CSP, but `img-src https:` and `frame-src http: https:` let a replay load remote resources and reveal that it was viewed |
| Capturing personal data | Depends on config | Masking runs in the browser only. Page text is masked only when the site or `SUPERMETRY_REPLAY_MASK=true` asks for it (`internal/replay/settings.go:240-250`); inputs are always masked |
| Rate-limit evasion over IPv6 | Easier than ingest | The replay key uses the full address, not /64 (`internal/replay/handler.go:387-390`) |

### Residual risk and next steps

| Residual risk | Severity | Next step |
|---|---|---|
| Quota checked hourly, CSS outside the quota | Medium | Check quota at upload time and count CSS toward it |
| Empty Origin accepted | Medium | Strict origin mode now exists for event ingest (see surface 1); apply the same requirement to replay intake |
| Replays can load remote resources in the player | Low | Proxy or strip remote URLs in the player, or tighten `img-src` and `frame-src` on playback routes |
| IPv6 keyed per address | Low | Use /64 as ingest does |

---

## 3. Share links and share passwords

**Surface.**

- Owners and admins create shares with `POST /api/private/sites/{site}/shares`.
- Viewers use `/share/{slug}` in the SPA, which calls `POST /api/private/share/{slug}/open` and `GET /api/private/share/{slug}/stats/summary`.

**Who can reach it.** Anyone with the link, and the password if one is set.

### Controls in place

| Control | Reference |
|---|---|
| Slug is `shr_` plus 32 random bytes; only its SHA-256 is stored | `internal/platform/shares/shares.go:100`, `internal/platform/crypto/tokens.go:11-30` |
| Optional password, Argon2id, with the same policy as accounts (10 or more characters, not on the common list) | `internal/platform/shares/shares.go:103-106`, `internal/platform/crypto/password.go:44-52` |
| Password attempts limited to 5/min per client and 20/min per share (in memory) | `internal/platform/shares/shares.go:146-150,237-250` |
| After unlock: HMAC-signed cookie bound to the slug, 12 h lifetime, key derived from `SUPERMETRY_SECRET_KEY` | `internal/platform/shares/shares.go:24-27,85-91,297-327` |
| Revocation is checked on every request | `internal/platform/shares/shares.go:174-186` |
| Share principal is limited to `stats:read` on one site; export negotiation requires `data:export` | `internal/platform/authz/authz.go:109-113`, `internal/platform/authn/authn.go:88-100`, `internal/api/router.go:623-628` |
| Exposed data: one fixed summary (visitors, visits, pageviews, bounce, duration, engagement, scroll) for a chosen period | `internal/platform/http/public.go` (`shareStats`, `statsResponse`) |

### Abuse cases

| Abuse | Feasible today? | Why |
|---|---|---|
| Guess a slug | No | 256-bit random |
| Brute-force a share password | Slow | 20 attempts/min per share across all clients is about 29k/day. A weak but policy-compliant password can fall within weeks |
| Lock viewers out of a password-protected share | Yes, while the attack runs | The same per-share bucket (20/min) is shared by every client, so wrong guesses from a few IPs use it up |
| Leaked link keeps working | **Yes, until revoked** | Shares have no expiry (`internal/platform/shares/shares.go:95-130` stores none) |
| Exfiltrate detail through a share | Limited | Only summary metrics for one site. No breakdowns, profiles or replays |

### Residual risk and next steps

| Residual risk | Severity | Next step |
|---|---|---|
| No expiry | Medium | Add an optional `expires_at` and show the last-used time |
| Unlock cookies survive a password change | Low | Shares have no password-change path today; revoke and recreate instead. If one is added, bind the cookie to a password version |
| Limiter is per process | Low | Document it; use the proxy for multi-instance deployments |

---

## 4. Login, signup, password reset, invites, 2FA, setup

**Surface.**

- `POST /api/private/auth/login`, `.../auth/signup`, `.../auth/password-reset`, `.../auth/password-reset/confirm`, `.../setup`
- `.../invitations/{token}/accept`
- 2FA is enabled from the account settings

**Who can reach it.** Anyone.

### Controls in place

| Control | Reference |
|---|---|
| Argon2id (t=3, 64 MiB, p=2, 16-byte salt) | `internal/platform/crypto/password.go:17-21,55-70` |
| Password policy: 10 or more characters, not on the embedded common-password list (only 11 entries; no breach check). Applied at signup, setup, reset, invite and share creation, **but not on password change** (`ChangePassword` hashes the new password without calling `ValidateNewPassword`) | `internal/platform/crypto/password.go:44-52`, `internal/platform/crypto/common_passwords.txt`, `internal/platform/auth/account.go:147-165` |
| Unknown email still runs a dummy Argon2 verify (timing parity) | `internal/platform/auth/login.go:122-125` |
| Login limits: 20/min per client, 3/min per account + client, 30/min per account across clients (5/min if no client key). In memory, per process | `internal/platform/auth/ratelimit.go:11-16,84-108` |
| Failed logins are audited as `auth.login_failed`, with a client hash keyed by the secret | `internal/platform/auth/login.go:107,164-188` |
| Signup is off unless `SUPERMETRY_SIGNUP=open` (404 otherwise). Default `invite` | `internal/platform/http/handlers_auth.go:89-93`, `internal/config/config.go:27,179` |
| First-user setup needs a one-time token; the setup URL is generated only while no user exists | `internal/platform/auth/setup.go:21-60` |
| Login, signup and setup require `Origin`/`Referer` to match `SUPERMETRY_BASE_URL` | `internal/platform/http/private.go:493-495,511-531` |
| Reset token: 32 random bytes, SHA-256 at rest, 1 h lifetime, single use; completion deletes all the user's sessions | `internal/platform/auth/reset.go:17,33-37,52-95` |
| Reset response is always 202; per-target limit 5/min | `internal/platform/http/handlers_auth.go:207-216`, `internal/platform/auth/ratelimit.go:66-82` |
| Invites: hashed token, 7-day lifetime, consumed under an org lock, email-bound for existing users, inviter must still be a manager | `internal/platform/invites/invites.go:23,100,158-200` |
| TOTP: time step recorded so a code is not reused; recovery codes hashed and single use; sensitive actions re-verify password and TOTP | `internal/platform/auth/totp.go:78-110` |
| Sessions: random token, SHA-256 at rest, 90-day absolute and 14-day idle expiry; `DELETE /api/private/me/sessions` revokes all | `internal/platform/auth/login.go:209-226`, `internal/platform/auth/sessions.go:15` |
| CLI recovery: `supermetry user reset-password --email` prints or sends a reset link | `cmd/supermetry/user.go:25-46,102-121` |

### Abuse cases

| Abuse | Feasible today? | Why |
|---|---|---|
| Credential stuffing from one IP | Blocked | 20/min per client |
| Distributed credential stuffing | Slowed | 30/min per account; no global login ceiling and no CAPTCHA |
| Lock a victim out | **Partly** | One client cannot (3/min per account + client), but about 10 or more clients together can keep the 30/min account bucket empty for as long as the attack runs. The lockout is soft and ends a minute after the attack stops |
| Online TOTP guessing by someone who has the password | Slow but real | TOTP shares the login buckets, so 30 guesses/min per account from many clients. The window is ±1 step (`internal/platform/auth/totp.go:173`), so about 3 codes are valid at any time. At 43,200 guesses/day that is roughly a 12% chance per day of a hit (*inference*, simple arithmetic) |
| Weak password set through "change password" | Yes, by the user themselves | No policy on change (`internal/platform/auth/account.go:147-165`) |
| Memory pressure from login floods | Possible | Each verify allocates 64 MiB; there is no concurrency cap on Argon2. Per-client limits bound it only per IP |
| Account enumeration | Limited | Login and reset do not reveal it. Open signup returns 400 for an existing email (`internal/platform/auth/signup.go:54-58`). Reset does extra work (DB insert, SMTP send) for real accounts, which is a timing signal |
| Signup with someone else's email | Yes, in open mode | No email verification step |
| Setup takeover before the first user | Requires the setup token | Token-gated |

### Residual risk and next steps

| Residual risk | Severity | Next step |
|---|---|---|
| Distributed soft lockout | Medium | Allow a successful login with a valid second factor or from a known session device to bypass the account bucket; add a metric for limiter rejections (none today) |
| TOTP shares the login budget | Medium | Give TOTP its own tighter per-account limit, separate from passwords |
| Password change skips the policy | Medium | Call `ValidateNewPassword` in `ChangePassword`; grow the common-password list |
| No Argon2 concurrency cap | Medium | Add a semaphore sized to memory |
| Open signup without email verification | Low (opt-in) | Verify email before activation |
| In-memory limits are per process | Low | Document it; use the proxy in multi-instance deployments |

---

## 5. API keys, public query API, MCP

**Surface.**

- `/api/public/v1/*`: query, explore, realtime, sites, goals, annotations, funnels, segments, profiles, erasures, api-keys.
- `POST /api/public/v1/mcp`.
- Keys are managed under `/api/private/orgs/{org}/api-keys`.

### Controls in place

| Control | Reference |
|---|---|
| Key format `pnl_<prefix>_<secret>`: 32 random bytes; SHA-256 at rest; constant-time compare | `internal/platform/keys/keys.go:194-227,333-372` |
| Scopes: `stats:read`, `data:export`, `profiles:read`, `replay:read`, `goals:write`, `annotations:write`, `segments:write`, `sites:write`, `keys:manage`. Optional site list and expiry | `internal/platform/authz/authz.go` (`keyScopes`), `internal/platform/keys/keys.go:81-149` |
| Keys cannot mint keys with wider scopes, sites or expiry than their own | `internal/platform/keys/keys.go:151-175` |
| Revocation and expiry checked on every request (DB lookup, no cache); revocation audited | `internal/platform/keys/keys.go:214,229-276` |
| Per-credential rate limit 2/s, burst 30; anonymous failures also limited per IP | `internal/api/router.go:91,103,527-568` |
| Query guard: 366-day range, 3 dimensions, 10 filters, 50 values, limit 1,000; `statement_timeout` hard-coded at 10 s for explore, funnel, retention, vitals and heatmap queries | `internal/query/guard.go:20-27,467-480`, `internal/query/sql_closed.go:21`, `internal/heatmap/query.go:51` |
| Unknown sites return 404 before scope errors (no site enumeration) | `internal/api/router.go:614-618` |
| MCP has read-only tools only: `list_sites`, `get_summary`, `get_timeseries`, `get_breakdown`, `get_realtime`, `list_goals`, `run_funnel` | `internal/api/mcp.go:619-625` |
| Anonymous tracking mode substitutes non-public paths in query results | `internal/api/router.go:105-107` |

### `SUPERMETRY_ADMIN_API_KEY`

This static bearer token grants all scopes on all sites:

- In benchmark compatibility mode, before any user exists (`internal/api/auth.go:38-49`, `cmd/supermetry/serve.go:176-185,311-316`).
- At all times, it bypasses CSRF on the private API (`internal/platform/http/private.go:497`) and acts as the instance replay admin (`internal/platform/http/handlers_replay.go:457-460`).

It cannot be revoked except by changing the environment and restarting.

Keys shorter than 32 characters disable key auth (startup warning). As a bearer token the key also authenticates `/api/private/admin/*` as a hidden system user (`admin-key@system.invalid` in the audit log). It cannot call `update/apply`, which needs the admin's password. Through that route set the key can delete organizations, change site retention, create users and start impersonation sessions (`POST /api/private/admin/users/{user}/impersonate`, which returns a token).

Impersonation controls: the session lasts 1 hour with a hard expiry; the UI shows a banner; the session cannot use admin routes, account-security actions (password, email, two-factor, account deletion, session revocation), API keys, invitations or their acceptance, member role changes, share links, notification channels, alerts or scheduled reports, so nothing it creates keeps acting after it ends; it is refused for instance admins, disabled users and users who must reset their password; it is audited in the instance audit and in each of the user's organizations; and it ends if the admin is demoted, disabled or deleted. Organization audit entries written during the session carry `impersonated_by` (the admin's user id). Cookie-based impersonation requires re-entering the admin password. A key-path token is a normal session cookie: mutating calls need the `supermetry_csrf` cookie and matching `X-CSRF-Token` header. Sessions started with the key survive key rotation for up to their 1 hour limit. Admin organization deletion and retention changes re-check instance admin inside their transaction.

### Abuse cases and residual risk

| Abuse / residual risk | Severity | Next step |
|---|---|---|
| A leaked key with `stats:read` reads all data for its sites until revoked; with `data:export` or `profiles:read`, more | Medium | Encourage site-limited, expiring keys in the UI; show last-used time (stored, `internal/platform/keys/keys.go:46,297`) |
| Expensive queries: 30-request burst × 10 s statement timeout, and no global query concurrency cap (*not found*). Session-authenticated `/api/private/query` and friends have no rate limit at all (`internal/api/router.go:151` uses `authenticate`, not `authenticateRateLimited`) | Medium | Add a per-instance query semaphore and a per-user limit |
| `SUPERMETRY_QUERY_TIMEOUT` is parsed (`internal/config/config.go:281`) but nothing reads `cfg.QueryTimeout`; the timeout is the 10 s constant | Low | Wire it through or remove it |
| Leaked `SUPERMETRY_ADMIN_API_KEY` is instance-wide, has no revocation path, and can now manage organizations, users and impersonation | High if set | Leave it unset in production; scope it, or drop it after setup |
| Rate limiter is per process; `RateLimit-*` headers are approximate | Low | Document it |

---

## 6. Imports, exports, backup and restore, secret key

| Item | Control | Reference |
|---|---|---|
| Import (Plausible ZIP, Umami CSV) | Owner/admin only, identified mode only; 512 MiB upload; ZIP limits of 1,000 entries, 2 GiB per entry, 8 GiB total | `internal/platform/http/handlers_imports.go:24,120-176`, `internal/imports/types.go:11-15` (helper-reported) |
| Export | Owner/admin or the `data.export` capability; aggregates only (pages, sources, geo, devices, UTM, events); audited as `site.exported` | `internal/platform/http/handlers_imports.go:243-278`, `internal/siteexport/export.go` (helper-reported) |
| CSV injection | Cells starting with `= + - @ \t \r` are prefixed with `'` | `internal/csvsafe/csvsafe.go:7-15` |
| Backup | `pg_dump` in a tar.gz, mode 0600. Excludes the secret key, GeoIP files and replay blobs | `cmd/supermetry/backup.go:156-160` |
| Restore | Requires `--yes`; checks entries and schema version; single transaction | `cmd/supermetry/backup.go:247-316` (helper-reported) |
| Secret key | At least 32 bytes base64, required at startup. HKDF per purpose, then AES-256-GCM | `internal/config/config.go:170-174`, `internal/platform/crypto/secretbox.go:15-60` |

**What `SUPERMETRY_SECRET_KEY` protects:**

- Sealed with it: TOTP secrets, alert channel URLs and tokens, and the replay S3 secret.
- HMAC-keyed with it: share unlock cookies and failed-login client hashes.
- Not protected by it: passwords (Argon2id), and session, API-key, share, reset and invite tokens (SHA-256). Salts are plaintext.

`supermetry migrate-labels` (`cmd/supermetry/migrate_labels.go:34-37`) re-seals stored secrets from the pre-rename labels under the **same** key. It is not key rotation.

| Residual risk | Severity | Next step |
|---|---|---|
| No secret key rotation: a leaked key decrypts every sealed secret in any DB copy, and replacing it breaks TOTP and alert channels | Medium | Add a `rotate-secret-key` command (old key + new key, re-seal) |
| Backups are unencrypted and contain password hashes, sealed secrets and the last two days of salts | Medium | Document encrypting backups at rest; consider `--encrypt` |
| Restore does not check authenticity | Low (operator only) | Sign the manifest |
| CSV guard misses a leading space or `\|` | Low | Extend the prefix set |
| Exports can carry PII from URLs and UTM values | Low | Document it |

---

## 7. Outbound requests (webhooks, alerts, site icons, GeoIP, SMTP)

| Client | Guard | Reference |
|---|---|---|
| Generic webhook, Slack, Discord alerts | `safehttp` dial guard; 5 s timeouts; redirects not followed; body read capped at 4 KiB. Generic webhooks are HMAC-SHA256 signed (`X-Supermetry-Signature`) when a secret is set | `internal/alerts/webhook.go:52-105,156-162` (helper-reported, guard confirmed) |
| Site icon discovery | `safehttp` with private access always off; 3 redirects, each re-validated; 256/512 KiB caps; triggered by owner/admin | `internal/sites/icon/discover.go:19-67` (helper-reported) |
| Telegram | Fixed host `api.telegram.org`, redirects off; no `safehttp` | `internal/alerts/telegram.go:47-62` (helper-reported) |
| GeoIP update | Fixed MaxMind URL; 200 MiB caps; SHA-256 verified; `Authorization` stripped on redirect | `internal/geoupdate/update.go` (helper-reported) |
| SMTP | Operator-configured host; STARTTLS default on, TLS 1.2 minimum | `internal/platform/mail/smtp.go` (helper-reported) |

`safehttp` (`internal/safehttp/safehttp.go:20-77`) resolves the host, rejects the destination if **any** answer is in a blocked range, then dials the vetted IP (no DNS-rebinding gap). The blocked ranges are:

- private, loopback, link-local (including 169.254.169.254), multicast and unspecified addresses
- 0/8, CGNAT 100.64/10, 192.0.0/24, 198.18/15, 240/4
- NAT64, Teredo, 6to4 and site-local IPv6

`SUPERMETRY_ALLOW_PRIVATE_WEBHOOKS=true` turns the block off for webhooks.

| Residual risk | Severity | Next step |
|---|---|---|
| An org admin can make the server send POSTs to any public host (open relay for small requests) | Low | Expected for webhooks; rate-limit test sends |
| `SUPERMETRY_ALLOW_PRIVATE_WEBHOOKS=true` exposes the internal network and metadata endpoints to every org admin | High if enabled | Replace it with an explicit host or CIDR allowlist |
| Slack and Discord deliveries are unsigned | Low | Inherent to those APIs |
| Documentation ranges (192.0.2/24, 2001:db8::/32 …) not blocked | Low | Add them |
| Icon fetch falls back to plain http; no refetch rate limit | Low | Limit refetches |

---

## 8. Dashboard SPA: headers, cookies, CSRF

| Control | Reference |
|---|---|
| CSP on every response: `default-src 'self'`; `script-src 'self'` + one hash; `style-src 'self' 'unsafe-inline'`; `img-src 'self' data: blob: https:`; `connect-src 'self'`; `frame-src 'self' blob: https: http:`; `object-src 'none'`; `base-uri 'self'`; `form-action 'self'`; `frame-ancestors 'none'` | `cmd/supermetry/serve.go:510-523` |
| `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`; HSTS (1 year) when `SUPERMETRY_BASE_URL` is https | `cmd/supermetry/serve.go:516-520` |
| Session cookie `supermetry_session`: HttpOnly, SameSite=Lax, Path=/, Secure when the base URL is https or the request is TLS / `X-Forwarded-Proto: https` | `internal/platform/http/handlers_auth.go:171-174,271-286` |
| CSRF: double-submit (`X-CSRF-Token` header must equal the `supermetry_csrf` cookie) on every non-GET under `/api/private/`, in both the platform mux and the analytics router | `internal/platform/http/private.go:487-509`, `internal/api/router.go:598-606,674-681` |
| CSRF-exempt paths: login, signup, setup (all three need an Origin match), password reset, invite accept, share open | `internal/platform/http/private.go:546-553` |
| Server timeouts: read header 5 s; read 30 s and write 60 s by default (`SUPERMETRY_HTTP_READ_TIMEOUT`, `SUPERMETRY_HTTP_WRITE_TIMEOUT`); idle 60 s | `cmd/supermetry/serve.go:500-507` |
| `/metrics`: bearer `SUPERMETRY_METRICS_TOKEN`, or loopback only | `internal/api/metrics.go:14-33` |
| No pprof in the binary (*grep found none*) | — |

| Residual risk | Severity | Next step |
|---|---|---|
| The CSRF token is not bound to the session; anyone who can set cookies on the domain (a sibling subdomain) can plant a matching pair | Low | SameSite=Lax already blocks cross-site POSTs; still, bind the token to the session with an HMAC |
| `style-src 'unsafe-inline'` and `img-src https:` weaken XSS containment | Low | Tighten after an audit of inline styles |
| HSTS without `includeSubDomains`; no `Permissions-Policy`; `MaxHeaderBytes` at the Go default (1 MiB) | Low | Add them |
| `/metrics` exposed through a same-host proxy when `SUPERMETRY_TRUSTED_PROXIES` is unset | Medium | Set `SUPERMETRY_METRICS_TOKEN` (runbook) and warn at startup |

---

## Top residual risks

1. **Forged events for any site.** Ingest trusts the body URL only (`internal/ingest/handler.go:145-149`). Up to 1,000 events/s per IP are accepted (`internal/ingest/ratelimit.go:143-144`), and there is no tool to delete stored spam by range or referrer.
2. **One shared value dictionary across sites** (`internal/dict/dict.go:46`). One site's spam can turn new values on every site into `Other`.
3. **Unset `SUPERMETRY_TRUSTED_PROXIES` behind a proxy.** `/metrics` becomes public (`internal/api/metrics.go:23-24`), visitors merge, and the per-IP limit becomes shared. There is no warning.
4. **Replay disk use.** Quota is enforced hourly only (`cmd/supermetry/serve.go:696`), with 64 MiB/min per site, an empty Origin accepted (`internal/replay/handler.go:463`), and CSS outside the quota.
5. **Distributed login pressure.** The 30/min per-account ceiling (`internal/platform/auth/ratelimit.go:16`) allows a soft lockout, gives online TOTP guessing to someone holding the password, and Argon2 has no concurrency cap.

Also noted: share links never expire; the secret key cannot be rotated; backups are unencrypted.

## Not verified

- Production deployment shape (proxy, TLS termination, Postgres pool size). The query-concurrency risk depends on the pool size, which we did not check.
- Items marked *helper-reported* were collected in a separate code-reading pass and spot-checked (backup exclusions, safehttp ranges, CSV guard, the replay retention job, dictionary cap), but not every line was re-read.
- Some auth facts (11-entry common-password list, unused `SUPERMETRY_QUERY_TIMEOUT`, missing policy on password change, ±1 TOTP window) came from the helper pass and were re-checked in code. Its other claims (setup token printed at every start with no TTL, invite link returned to the inviter even when emailed, sibling reset tokens not cleared) were not re-checked and are left out.
- That `store.ConsumeInvitation` marks an invite as used: inferred from its name and call site, SQL not read.
- Behaviour of the SPA beyond the share page and the CSP: no browser testing was done.
