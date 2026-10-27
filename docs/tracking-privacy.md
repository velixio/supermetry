# Tracking and privacy enforcement

This phase adds collection and read controls to the existing single Go binary,
embedded React UI and PostgreSQL design. It does not delete existing analytics
when tracking mode changes. No production instance is changed by this branch.

## Installation and changes

`SUPERMETRY_TRACKING_MODE` accepts exactly `anonymous` or `identified`. Unknown values,
case variants, whitespace and an explicitly empty value fail startup validation.
`config.LoadLookup` preserves absent versus explicit; production
commands use `os.LookupEnv`. The legacy value-only `config.Load` helper cannot
distinguish absent from empty and is not the process startup boundary.

The default is `identified`: when the variable is unset the server collects real page
paths, referrers and campaigns, and new projects are created with profiles on. Replay stays
off, and nothing uses cookies or stores raw IP addresses. Set
`SUPERMETRY_TRACKING_MODE=anonymous` for the strictest mode. The server logs the active mode
at startup and says so when identified comes from the default.

Upgrading an install that left the variable unset therefore moves it from anonymous to
identified. Existing projects keep their own profiles setting; only new projects start with
profiles on. An explicit choice never rewrites project configuration or purges existing rows.

Mode is fixed for the process lifetime. To change mode: stop intake on every
instance, drain/stop old writers and jobs, set the mode consistently, then restart
all instances and resume traffic. This is a maintenance boundary, not a rolling
upgrade protocol. Do not mix versions/modes. There is no distributed policy
barrier, durable invalidation epoch, or proof that a downgrade has completed
across independently running instances.

## Collection controls

Explicit identity collection requires **identified instance permission AND
project profiles opt-in**. Project settings cannot enable profiles in an anonymous
instance. Settings show configured opt-in, instance mode, effective mode and
restriction reasons. A profile flag alone is not a guarantee of identity-free
payloads.

When the identity gate is closed, intake suppresses profile identifiers/traits
and identify events, drops custom properties, and removes URL/referrer credentials,
query parameters and fragments. Queued event writes recheck current cached site
permission before each flush/retry and remove identity links, traits, properties
and already-interned referrer/campaign dimensions; session flushes clear campaign
dimensions too. Unknown/deleted sites fail closed. Policy checks use memory, not
DB reads on ingestion. A transaction already underway can finish under its prior
snapshot: use the maintenance boundary for strict downgrade completion.

**Remaining payload limits:** hostnames and event names may themselves contain
identity. New paths on an anonymous instance use the explicit substitution contract below.
On an identified instance real paths are stored for every site, whatever its
profiles setting (query string and fragment are still dropped), and imported or
legacy paths are not rewritten. Arbitrary text cannot be proven anonymous by stripping a profile key.
The controls therefore do not promise personal-data-free storage. Anonymous
imports and raw exports are denied; trusted operators remain responsible for
what application code places in URLs, event names and replay content.

GPC (`Sec-GPC: 1`) and DNT (`DNT: 1`) drop event and heatmap submissions regardless
of the old project `respect_privacy_signals` flag. Browser collection also honors
those signals and visitor opt-out. Tracker consent denial/withdrawal gates the
common upload path and heatmap buffers; the base transport must not bypass consent
through a direct extension upload.

### Strict origin and blocked referrers

Two per-site, opt-in controls reduce forged events (Site settings, Tracking):

- **Strict origin** (`strict_origin`, off by default). The normal check trusts the page URL inside the event, so anyone who knows a site ID can post events with an allowed hostname. With strict origin on, the request must also carry an `Origin` header (or, if absent, a `Referer`) whose host is one of the site's allowed hosts. Other requests are dropped with a normal `202` response and counted in `ingest_origin_rejected_total`; no error is returned, so a sender cannot learn what to change. Browsers always send `Origin` on cross-origin POSTs, so the tracker script is unaffected. **Server-side senders and other non-browser clients usually send neither header, so turn this off for sites that use them.** A script can still fake the header, so this raises the cost of spam but is not authentication.
- **Blocked referrers** (`blocked_referrers`, up to 200 domains). Events whose referrer is on the list, or a subdomain of a listed domain, are dropped as spam-referrer traffic, exactly like the built-in list. See `SUPERMETRY_REFERRER_SPAM_FILE` in the configuration guide for an instance-wide list.

## Replay is independent

Replay requires **instance `SUPERMETRY_REPLAY_ENABLED=true` AND project replay opt-in**.
Identified mode alone never enables it. Anonymous mode does not automatically
forbid an explicitly enabled recorder; ordinary historical replay reads remain
blocked in anonymous mode because recordings can contain identity.

Masking combines with OR. Sample, retention and quota settings use the smaller
explicit cap; stricter project choices survive environment configuration. Settings
responses expose configured/effective values, instance caps and restrictions;
API updates that exceed instance policy are rejected. A zero sample rate disables
all recording, including triggers. Triggers do not override the sampling ceiling.
The previous unsampled trigger ring is consequently inactive in this candidate:
production admission selects full recording or off, and the UI disables trigger
controls without rewriting stored values. Restoring trigger-only recording needs
an explicit mode within the sampled cohort; current browser trigger tests exercise
client compatibility, not a production trigger mode.

Replay start, chunk and CSS upload admission recheck cached project permission,
origin, GPC/DNT, required consent assertion and current settings. The recorder
receives a policy fingerprint and sends it with uploads; missing/stale fingerprints
are rejected after a settings change. This prevents an older recorder from
continuing under superseded settings. It is not a secret or proof of user consent.

**Replay limits:** consent is a client assertion, not a cryptographically verified
consent receipt. Masking runs in the recorder; the server accepts opaque compressed
recordings and cannot prove a forged payload was masked. Required masking and
fingerprint checks are not server-side content anonymization. Quota/retention are
resolved configuration ceilings consumed by the existing asynchronous cleanup;
active recordings and concurrent writes can exceed instantaneous storage targets.

**Replay retention caps:** a site's effective replay retention is the smallest of
`SUPERMETRY_REPLAY_RETENTION_DAYS` (when set), its organization's cap (when set)
and the site's own `retention_days`. An instance admin sets the organization cap,
1 to 365 days, with `PUT /api/private/admin/organizations/{org}/replay-retention-cap`
(see the instance admin API guide), so a hosted deployment can give each plan its
own retention; no cap is the default and changes nothing. The settings API reports
`org_retention_capped` when the organization cap is what lowers the value, and the
site's replay settings page shows the organization maximum. The hourly cleanup
enforces the effective value, so **lowering a cap deletes recordings older than the
new retention at the next run**; raising the cap later does not bring them back.
The cleanup logs one line per site per run (`replay retention`) with counts only:
site id, effective days, the organization cap, sessions deleted for age and sessions
kept.
A strict byte admission quota and durable deletion protocol remain separate work.

## Historical reads

Anonymous mode blocks ordinary profiles, session/event detail, raw replay and
assets, property values, free-form exploration, raw exports and imports. Guards
apply to HTTP/private/public routes, query engine entry points before cache,
MCP tools/resources and report/export boundaries; authorization remains required
in every mode. Switching back to identified restores only access already permitted
by the existing tenant/site/capability authorization model.

Counts and time-series remain available. Canonical country, device, browser, OS
and channel breakdowns are supported through a finite safe-value allowlist; unknown
legacy/imported labels do not escape as arbitrary text. Filters are limited to
nonempty canonical values on those safe dimensions using `is` or `is_not`. Unknown
labels are coalesced to the empty bucket before finalizing metrics; selecting that
bucket as a filter is restricted because legacy labels cannot be selected by a
single canonical equality. Query caches are separated by instance mode, path sanitizer version and policy
fingerprint. General page/referrer/source/UTM/property/event-name dimensions and
filters remain denied, except for the bounded verified page view below.
Reports omit page/source breakdowns in anonymous mode. Custom report/query paths
cannot bypass the engine policy through a previously populated cache.

### Verified page paths (bounded v1 slice)

`SUPERMETRY_TRACKING_PUBLIC_PATHS` is a JSON array of exact public path literals, for
example `["/", "/pricing", "/docs"]`. Absent or `[]` declares no paths public.
Explicit empty/malformed input fails startup. At most 128 entries of 512 UTF-8
bytes each are accepted. No patterns, prefixes, parameter capture or identifier
heuristics are used. Operators must declare only literals suitable for exposure;
this is a publication decision, not a personal-data detector.

For effective anonymous collection, exclusions inspect the original path first.
Then the server replaces the path with an exact declared literal or the single
`/[redacted]` fallback before dictionary interning, session updates and heatmap
collection. Credentials/query/fragment are omitted from the path; encoded and
noncanonical paths fall back. Original path segments never appear in the fallback.
The Sessions interface displays the stored path; it does not reconstruct or
replace the fallback label. Setting the instance to identified alone does not
change collection for a project with profiles disabled: both permissions are
required for unsanitized new paths. After a coordinated restart with both enabled,
new sessions may retain their actual paths; older `/[redacted]` values remain
redacted because the original path was discarded, not merely hidden.

The path evidence makes no assertion about retained hostnames, event names or
opaque replay payloads. Campaigns and properties do not gain verified provenance.

Only pageview events receive server-issued `path_sanitizer_version=1` and a
SHA-256 fingerprint of the version and literal set. Client fields cannot establish
trust. Queued writes revalidate the version, current fingerprint and cached safe
label; invalid/unverified buffered paths become ID zero without being promoted.
Old events and CLI imports default to version zero even when their text happens
to match a public literal. The maintenance loader can still intern raw import
text in the dictionary; its events remain unverified and do not enter this view.

Migration 0027 adds event evidence and separate `verified_page_sessions` and
`verified_page_day` tables. All keys and aggregation groups carry the version and
policy fingerprint. Ordinary closed sequences, day/month/block rollups, sessions,
replay and imported aggregates are never used as evidence for safe page reads.
The event-only closed projection supports rebuilding the verified daily rollups
after raw partitions expire. A reclose that would lose existing verified groups
fails explicitly; use rollup recomputation from retained closed records instead.
No timestamp or matching dictionary value retroactively grants trust.

The anonymous query/API/MCP view accepts exactly one `page` or `path` dimension,
no filters, and only `visitors`, `visits`, and/or `pageviews`. It supports sorting,
pagination, comparisons and query CSV. CSV filenames carry `-verified-only`;
`X-Supermetry-Privacy-*` response headers carry the scope, version, policy and exclusions.
The CSV body keeps its existing columns and does not embed JSON metadata. Dates use the site's time zone; another
requested time zone is rejected. Visitors and visits mean daily visitors/sessions
with at least one verified pageview under the **current exact policy**. Range
counts add those daily counts. Totals are deduplicated across paths before
aggregation, not summed from path rows. Legacy, imported and other-policy cohorts
are excluded even in mixed date ranges. Metadata explicitly reports
`view=verified_paths`, `coverage=verified_only`, version, policy fingerprint and
unverified/import exclusions; it does not claim complete historical coverage.
A single SQL snapshot chooses hot versus closed data at the close boundary.
Cache keys include mode/version/fingerprint/close generation, and output labels
are validated against the current allowlist. Changed literal sets intentionally
exclude earlier-policy cohorts rather than relabeling them. Restoring the exact
previous policy restores its previously verified cohort, not unverified history.

Entry/exit, bounce/duration/engagement/revenue, combined dimensions, page filters,
page time-series, event/property/campaign exploration and raw exports remain
restricted. Dashboard cards and scheduled reports have not been redesigned for
this partial-population view; anonymous reports still omit page/source sections.
This is a bounded API slice, not restoration of all anonymous analytics. No
session-wide or replay anonymization is claimed.

Administrative configuration (site names, saved definitions, account information)
is not analytics payload redaction. This policy does not rewrite every string
in the application or replace account/security authorization. Maintenance-level
database access and operator backup commands are outside this HTTP analytics
read policy; protect their credentials and artifacts separately.

## Erasure and retained data

Downgrade retains existing rows/blobs. Ordinary historical identity reads are
blocked, while the separately authorized profile deletion endpoint remains
available and checks tenant/site/action permissions. Its current interface needs
a known profile ID; a purpose-specific erasure lookup/UI is not implemented.
Tests verify denied reads leave a synthetic profile intact before an explicit
authorized deletion. Do not infer durable multi-instance erasure guarantees from
this endpoint's existing process-local tombstones. Verified closed page/session
links are removed when the existing profile-erasure linkage can identify them;
aggregate counts retain the existing `aggregates_adjusted=false` contract.
Erasure and closing share a database lock. If identity-bearing raw events have
already expired, durable profile-to-session discovery remains unavailable.
Linked replay blobs and index rows are removed before the profile-erasure
transaction commits. A replay-storage failure rolls back the database erasure,
clears its process-local tombstone and leaves the same authorized request
retryable. Blob storage and PostgreSQL cannot share one atomic transaction, so a
successful blob deletion cannot be rolled back. A later blob or database failure
can therefore leave an already deleted replay blob while PostgreSQL rolls back;
the retained replay index makes the request retryable, and supported blob stores
treat deleting that now-absent blob as success. This is privacy-safe partial
deletion, not a successful erasure response. Buffered
anonymous writes, concurrent replay uploads, cross-instance resurrection,
restart-safe deletion jobs, full retry/ordering guarantees and cross-instance
tombstone propagation still need design.

## Verification and rollout status

Tests cover strict mode parsing, the identified default, retained configuration,
forged profile/property submissions, buffered downgrades/retries, mandatory privacy
signals including heatmaps, both replay switches, stricter settings, stale tracker
policies, consent withdrawal, restricted history, safe canonical breakdowns,
cache boundaries, MCP/reports/export and continued authorized deletion.

Verification uses isolated synthetic PostgreSQL and real Go/Bun/Chromium tools.
Existing tests that exercise identified features explicitly select identified
fixtures; privacy tests exercise the anonymous zero value, which parsing no longer returns by default. Existing backpressure
20 ms timing and UTM closed-storage failures are reported separately if observed;
their thresholds and assertions are unchanged. The user approved a specific
core tracker gzip budget increase for required privacy/consent controls: 1004-byte
baseline, 1067-byte artifact (+63 bytes, about 6.3%), 1100-byte regression cap.
The size test remains enabled; replay and heavy features stay separate.
Optional S3/backup fixture skips are
not coverage. See the candidate handoff for exact commands/results.

This is a local review candidate, not a deployment or approval to change any
live mode, delete historical data, or roll out mixed-version instances. Review
all documented restrictions and remaining payload/replay limitations before
publication or activation.


Verified-path validation additionally covers forged client markers, exclusion
ordering, literal/fallback collection before interning, queued policy mismatch,
zero-trust imports, policy-separated close/reclose, actual raw-partition expiry,
rebuild from durable projections, distinct totals, comparisons/pagination,
cache policy changes and invalid dictionary/evidence pairs. No production
migration or configuration change has been run by this work.

## Dashboard and navigation policy

The authenticated site bootstrap includes the instance policy separately from
project profile opt-in. The interface uses the instance policy to gate historical
analytics before mounting query hooks. Missing policy shows an unavailable state;
it never grants identified access. Server authorization and privacy checks remain
the authority, including direct API requests.

Anonymous dashboards retain aggregate counts, trends and canonical breakdowns.
Verified page counts are a separate partial cohort: only current-policy verified
pages, with historical unverified data and imports excluded. They do not support
page filters, combined dimensions or session-derived page metrics. Unsupported
saved filters require removal before queries run; the interface does not silently
remove them and change the requested population. Restricted detail screens explain
the policy instead of requesting forbidden data. Terminal authorization failures
remain errors and stop automatic retries/live polling. Dashboard cache keys include
the instance mode so identified results cannot fill anonymous views.

Replay navigation depends on identified historical-read permission and the user's
replay grant, independently of whether new recording is enabled. The replay view
shows recording status and the project settings link; disabling collection does
not by itself erase recordings or revoke separately authorized historical reads.
