# Security policy

## Reporting a vulnerability

Please do not open a public issue for security problems.

Use GitHub's private vulnerability reporting: open the repository's **Security** tab and choose **Report a vulnerability**.

Include what you found, how to reproduce it, the version or commit, and the impact you expect. You can expect an acknowledgement within a few days. We will keep you updated on the fix and credit you in the release notes unless you prefer otherwise.

## Supported versions

Until `v0.1.0` is released, only the latest commit on `main` is supported. After that, the latest release gets security fixes.

## Scope

In scope: the Supermetry server, dashboard, tracker script and the official API client. Out of scope: vulnerabilities in your reverse proxy, PostgreSQL or host configuration, and reports that need access to a deployment's secret key or database.

## What is protected and what is not

The full analysis, with code references, is in [docs/security/threat-model.md](docs/security/threat-model.md). In short:

**Protected**

- **Passwords:** hashed with Argon2id.
- **Tokens stored as hashes:** sessions, API keys, share links, invites and password resets are stored only as SHA-256 hashes, so a database dump alone does not let anyone log in.
- **Secrets sealed with `SUPERMETRY_SECRET_KEY`:** TOTP secrets, alert channel credentials and the replay S3 secret.
- **Login:** rate-limited per IP and per account. Supports TOTP with single-use recovery codes.
- **Dashboard:** strict CSP, `frame-ancestors 'none'`, HttpOnly SameSite=Lax session cookies, CSRF tokens on every state-changing private API call.
- **Outbound requests:** webhooks, alerts and site icons cannot reach private, loopback or cloud-metadata addresses unless you set `SUPERMETRY_ALLOW_PRIVATE_WEBHOOKS=true`.
- **API keys:** scoped, can be limited to sites, can expire, and can be revoked immediately.
- **Share links:** expose only one site's summary numbers.
- **Visitor IPs:** never stored. Visitor IDs use a daily salt that is deleted after about 48 hours.

**Not protected**

- **Event ingest is unauthenticated by design.** The site ID in your tracking snippet is public. Anyone can send fake events for your site from a script, as long as the page URL in the event uses one of the site's allowed hosts. Rate limits, bot and referrer-spam lists, and exclusions reduce this but cannot prevent it.
- **Session replay intake is also unauthenticated**, when enabled.
- **Rate limits are in memory, per process.** Running several instances multiplies them. Use your reverse proxy for a shared limit.
- **The secret key cannot be rotated today.** If it leaks, treat every sealed secret as exposed.
- **Backups (`supermetry backup`) are not encrypted.** They contain password hashes, sealed secrets and recent salts. Store them encrypted.
- **Share links can expire**, but existing links and links created through the API without an expiry never do. Dashboard-created links default to 90 days. Revoke links you no longer need.

## Abuse runbook

This section is for operators of a Supermetry instance. Every variable, command and endpoint named here exists in the current code. A few things you might expect do not exist yet; they are listed at the end.

### 1. Recognise spam and floods

1. **Open the Data health page**: `/app/<org>/health` in the dashboard. It reads `GET /api/private/health`. The top counters are instance-wide (admins only). The **Dropped and throttled traffic** section shows, per site and per day, how many events were dropped by reason (bot rule, rate limit, privacy signal, dictionary overflow) with a **Spike** badge when today is far above the 7-day average. Only counts are stored.
2. **Scrape `/metrics`.** Set `SUPERMETRY_METRICS_TOKEN` and send `Authorization: Bearer <token>`. Without a token, `/metrics` accepts only a direct loopback connection that carries no forwarding headers (`X-Forwarded-For`, `Forwarded`, `X-Real-IP`, `CF-Connecting-IP`, `True-Client-IP`); everything else gets 401. The useful counters, all prefixed `supermetry_`:

   | Counter | A rise means |
   |---|---|
   | `ingest_accepted_total` | Accepted events. Compare with your normal traffic |
   | `ingest_rate_limited_total{reason="ip"\|"site"\|"global"}` | A flood. `site` points at one site; `global` means every site is affected |
   | `ingest_invalid_total` | Malformed events, unknown site IDs, or page URLs whose host is not allowed. Often someone scripting against a guessed or copied site ID |
   | `bots_filtered_total{reason=...}` | Crawler, headless or spam-referrer traffic that was dropped |
   | `ingest_excluded_total{reason=...}` | Events dropped by your IP, path or country exclusions |
   | `ingest_dropped_total`, `writer_queue_depth` | The write queue is full (503s). Load is above what the database can absorb |
   | `ingest_duplicates_total`, `ingest_late_dropped_total` | Replayed or backdated events |
   | `replay_bytes_total`, `replay_rejected_total`, `replay_dropped_total`, `replay_disk_low` | Replay upload volume and disk pressure |

3. **Check the dashboard itself.** Spam usually shows up as a sudden spike from one referrer, country, page or browser version, many `Other` values, or a bounce rate near 100%.
4. **Logs.** `SUPERMETRY_INGEST_DIAG=1` logs, for each ingest request, how the client address was resolved (masked), whether the peer was a trusted proxy (`peer_trusted`), and why an event was dropped as a bot. It is noisy, so turn it on only while investigating.

### 2. Event spam against one site

Work through these in order.

1. **Confirm `SUPERMETRY_TRUSTED_PROXIES` is correct.** If Supermetry runs behind a reverse proxy, a load balancer or Cloudflare, set this to the proxy's addresses or CIDRs, comma-separated. Loopback and private networks are trusted when it is empty, so only a proxy with a public address (Cloudflare, a cloud load balancer) needs listing. If such a proxy is not listed:
   - Every visitor appears to come from the proxy's IP. The per-IP limit (1,000 events/s) then applies to all visitors together, and one flooder can push real visitors into 429s.
   - Visitors with the same browser merge into one visitor.
   - IP exclusions and geo lookup see the proxy, not the client.
   - A proxy on the same host no longer makes `/metrics` public: requests that pass through it carry forwarding headers and are refused without `SUPERMETRY_METRICS_TOKEN`. The server logs a startup warning when `SUPERMETRY_TRUSTED_PROXIES` is `none` and it listens on loopback.

   Check it with `SUPERMETRY_INGEST_DIAG=1`: requests that arrive through the proxy should log `peer_trusted=true`. List only proxies you control. A trusted range lets anyone inside it choose their own client IP through `X-Forwarded-For`.
2. **Tighten the site's allowed hosts** (site settings in the dashboard, or `PATCH /api/private/sites/{site}` with `allowed_hosts`). Remove wildcard entries you do not need. This stops events whose page URL names another host. It does not stop a script that copies one of your real hostnames.
3. **Add exclusions** (`/app/<org>/<site>/settings/tracking`, or the `exclude_ips`, `exclude_paths` and `exclude_countries` fields of `PATCH /api/private/sites/{site}`). Excluded events are dropped before storage and counted in `ingest_excluded_total`. Exclusions apply only to new events, not to data already stored.
4. **Block at the proxy.** The ingest rate limits default to per IP 1,000/s, per site 5,000/s and per process 10,000/s; change them with `SUPERMETRY_INGEST_RATE_IP`, `SUPERMETRY_INGEST_RATE_SITE` and `SUPERMETRY_INGEST_RATE_GLOBAL` (bursts are twice the rate; a setting that breaks the order ip <= site <= global is clamped with a startup warning). For a lower limit, or to block an IP range or a referrer, do it in your reverse proxy or WAF on the event path (`SUPERMETRY_EVENT_PATH`, default `/api/public/v1/events`).
5. **Referrer spam.** Add the domain to the site's **blocked referrers** (Site settings, Tracking), or to an instance-wide file named by `SUPERMETRY_REFERRER_SPAM_FILE` (read at startup). The built-in list (`internal/enrich/lists/referrer-spam.txt`) changes only with a release. Report new spam domains to the maintainers (see section 8).
   Also consider turning on the site's **strict origin** setting: events must then carry an `Origin` or `Referer` from an allowed host. It does not stop a script that fakes the header, and it breaks server-side senders, so test it first. Rejected requests are counted in `ingest_origin_rejected_total`.
6. **Clean up data already stored.** The tools available today:
   - **Profile erasure** (identified tracking mode only): `DELETE /api/private/profiles/{profile}` removes one profile's events, sessions and replays and rebuilds the affected days.
   - **Retention** drops whole expired partitions. It cannot target spam.
   - **Delete stored traffic** (site settings, Tracking, owners and admins; `POST /api/private/sites/{site}/purge-requests`): removes the sessions and events that match a date range and a source and/or a country, then rebuilds the affected days. It needs the caller's password. Session recordings are not touched, and there is no filter by IP address or path.
   - **Deleting the site** (`DELETE /api/private/sites/{site}`) erases all of its data.

   If spam cannot be matched by source or country, add an annotation for the affected period so readers know to discount it.

### 3. Credential stuffing and account lockout

- **Built-in limits:** 20 login attempts per minute per client IP, 3 per minute per account per IP, and 30 per minute per account across all IPs. After the password is accepted, two-factor codes (and recovery codes) have their own limit: 5 wrong codes lock the second factor of that account for 15 minutes, and a correct code is refused during the lockout. The lockout is audited as `auth.totp_locked`. All limits are in memory and reset on restart.
- **Signs of an attack:** many 429 responses on `/api/private/auth/login`, and `auth.login_failed` entries in the organization audit log (`GET /api/private/orgs/{org}/audit`). Failed logins against unknown emails are not audited. There is no metric for login rate-limit hits.
- **What to do:**
  1. Check that `SUPERMETRY_TRUSTED_PROXIES` is correct. Otherwise every login shares the proxy's per-IP bucket, and an attacker can block all logins.
  2. Rate-limit or challenge `/api/private/auth/login` and `/api/private/auth/password-reset` at the proxy.
  3. Keep `SUPERMETRY_SIGNUP=invite` (the default) unless you need open signup.
  4. Ask users to enable two-factor authentication.
- **A user locked out by a distributed attack** stays locked out only while the attack runs; the account bucket refills within a minute after it stops. To give them a way in, run `supermetry user reset-password --email <address>`. It sends a reset link, or prints it when SMTP is not configured. Completing a reset signs out all of the user's sessions.
- **A suspected compromised account:** reset the password, which revokes every session. The user can also revoke sessions at `/app/account/sessions` (`DELETE /api/private/me/sessions`). Then review the org audit log for `api_key.created`, `share.created`, `invitation.created` and `membership.role_changed`.

### 4. Leaked API key

1. Revoke it in the dashboard at `/app/<org>/settings/api-keys`, or with `DELETE /api/private/orgs/{org}/api-keys/{key}`. Revocation is checked on every request, so it takes effect immediately.
2. You can also revoke it with another key that holds `keys:manage`: `DELETE /api/public/v1/api-keys/{key}`.
3. Check the key's last-used time (updated about once a minute) and the audit log for actions taken with it.
4. Issue a replacement with the smallest scopes, only the sites it needs, and an expiry date.

**`SUPERMETRY_ADMIN_API_KEY`** is different: it grants every scope on every site, bypasses CSRF, and cannot be revoked from the dashboard. If it leaks, remove or change the variable and restart. Leave it unset in production unless you need it.

### 5. Leaked share link

Revoke the share at `/app/<org>/<site>/settings/shares` (`DELETE /api/private/sites/{site}/shares/{share}`). It stops working on the next request, and existing unlock cookies stop working too. Links created with an expiry stop working on their own at that time (an expired link answers like a revoked one); links without an expiry work until revoked, so revoke links you no longer need. A share shows only that site's summary numbers: visitors, visits, pageviews, bounce rate, durations, scroll depth.

### 6. Suspected `SUPERMETRY_SECRET_KEY` leak

**What the key protects:**

- Encrypted with it: TOTP secrets, alert channel URLs and tokens (webhook secrets, Telegram bot tokens), and the replay S3 secret key.
- Used as a signing key: share-link unlock cookies and the hashed client IDs in the failed-login audit.

**What it does not protect:** passwords, sessions, API keys and share links do not depend on it.

A leaked key matters together with a copy of the database or a backup. There is no rotation command; `supermetry migrate-labels` only re-seals secrets from the old pre-rename labels under the *same* key, so it does not help here. What to do:

1. Generate a new key: `openssl rand -base64 32`.
2. Set it, then restart. The server will not be able to open secrets sealed under the old key, so:
   - TOTP codes from authenticator apps stop working. Users with two-factor authentication must sign in with a recovery code (those are hashed, not sealed), then disable and re-enable 2FA. There is no CLI to reset 2FA, so a user with no recovery codes cannot sign in.
   - We have not tested whether the server logs errors or refuses work when it meets values it cannot open. Try the new key on a staging copy first.
   - Re-enter every alert channel's URL, token and secret.
   - Re-enter the replay S3 secret.
   - Share-link unlock cookies become invalid, so password-protected shares ask for the password again.
3. Rotate the credentials that were sealed: webhook secrets, Telegram bot tokens, S3 access keys.
4. Treat existing backups as exposed together with the old key.

### 7. Replay disk abuse

Replay intake is off unless `SUPERMETRY_REPLAY_ENABLED=true`, and each site must also turn it on.

**Built-in limits:**

- 256 KiB per chunk, 64 MiB per minute per site, 64 uploads at a time.
- Per site, CSS is capped at 10,000 files or 512 MiB.
- Writes stop for **every** site when free disk drops below 2 GiB; this shows as `supermetry_replay_disk_low 1`.
- The storage quota (`SUPERMETRY_REPLAY_QUOTA`, default 5 GiB) and retention (`SUPERMETRY_REPLAY_RETENTION_DAYS`, default 30) are enforced by an hourly job, not at upload time, so a site can briefly go over its quota.

**If replay storage grows unexpectedly:**

1. Turn replay off for the site in its settings, or for the instance with `SUPERMETRY_REPLAY_ENABLED=false` and a restart.
2. Lower `SUPERMETRY_REPLAY_SAMPLE_RATE`, `SUPERMETRY_REPLAY_QUOTA` or `SUPERMETRY_REPLAY_RETENTION_DAYS`.
3. Put replay on its own volume (`SUPERMETRY_REPLAY_DIR`) or on S3 (`SUPERMETRY_REPLAY_S3_*`), so a full replay disk cannot fill the database disk.
4. Limit request rates on `/api/replay/` at the proxy.

### 8. Reporting abuse to the maintainers

- **Security vulnerabilities:** use private vulnerability reporting, as described at the top of this file.
- **New referrer-spam domains, crawlers the bot filter misses, or abuse patterns the built-in controls do not handle:** open a normal GitHub issue. Include the domain or user agent and the counters you saw. Leave out visitor data and your site IDs.

### Not available yet

These are being worked on and are **not** in the current release. Do not rely on them:

- Deleting events by time range, referrer or IP.
- Secret key rotation.
