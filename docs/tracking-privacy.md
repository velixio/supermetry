# Tracking and privacy enforcement

This phase adds collection and read controls to the existing single Go binary,
embedded React UI and PostgreSQL design. It does not delete existing analytics
when tracking mode changes. No production instance is changed by this branch.

## Installation and changes

`PANEL_TRACKING_MODE` accepts exactly `anonymous` or `identified`. Unknown values,
case variants, whitespace and an explicitly empty value fail startup validation.
`config.LoadLookup` preserves absent versus explicitly anonymous; production
commands use `os.LookupEnv`. The legacy value-only `config.Load` helper cannot
distinguish absent from empty and is not the process startup boundary.

Fresh installations default to anonymous, profiles off, replay off. At startup,
the server checks durable active-site settings (and benchmark compatibility sites)
before listening. If profiles are enabled and mode is unset, startup fails with
instructions to explicitly choose `identified` to continue collecting identity,
or `anonymous` for an intentional downgrade. An explicit choice never rewrites
project configuration or purges existing rows.

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

**Remaining payload limits:** paths, hostnames and event names are retained for
analytics and may themselves contain identity. Imported/legacy data is not
rewritten. Arbitrary text cannot be proven anonymous by stripping a profile key.
The controls therefore do not promise personal-data-free storage. Anonymous
imports and raw exports are denied; trusted operators remain responsible for
what application code places in URLs, event names and replay content.

GPC (`Sec-GPC: 1`) and DNT (`DNT: 1`) drop event and heatmap submissions regardless
of the old project `respect_privacy_signals` flag. Browser collection also honors
those signals and visitor opt-out. Tracker consent denial/withdrawal gates the
common upload path and heatmap buffers; the base transport must not bypass consent
through a direct extension upload.

## Replay is independent

Replay requires **instance `PANEL_REPLAY_ENABLED=true` AND project replay opt-in**.
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
single canonical equality. Query caches are separated by instance mode. Free-form
page/referrer/source/UTM/property/event-name dimensions and filters remain denied.
Reports omit page/source breakdowns in anonymous mode. Custom report/query paths
cannot bypass the engine policy through a previously populated cache.

There is no per-row provenance boundary yet. These free-form read restrictions
apply to new data as well as legacy data. Preserving safe canonical breakdowns
avoids disabling anonymous analytics wholesale, but raw exploration and free-form
dashboard cards may show restricted access. A future verified collection epoch
and sanitization contract could safely expand those reads; a timestamp alone
cannot prove that caller-supplied strings contain no identity.

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
this endpoint's existing process-local tombstones. Restart-safe deletion jobs,
retry/ordering guarantees and cross-instance tombstone propagation need design.

## Verification and rollout status

Tests cover strict mode parsing, explicit upgrade choices, retained configuration,
forged profile/property submissions, buffered downgrades/retries, mandatory privacy
signals including heatmaps, both replay switches, stricter settings, stale tracker
policies, consent withdrawal, restricted history, safe canonical breakdowns,
cache boundaries, MCP/reports/export and continued authorized deletion.

Verification uses isolated synthetic PostgreSQL and real Go/Bun/Chromium tools.
Existing tests that exercise identified features explicitly select identified
fixtures; privacy tests exercise the anonymous zero value. Existing backpressure
20 ms timing and UTM closed-storage failures are reported separately if observed;
thresholds and assertions are not weakened. Optional S3/backup fixture skips are
not coverage. See the candidate handoff for exact commands/results.

This is a local review candidate, not a deployment or approval to change any
live mode, delete historical data, or roll out mixed-version instances. Review
all documented restrictions and remaining payload/replay limitations before
publication or activation.
