# Tracking and privacy policy foundation

Status: inert primitives only. No runtime enforcement, configuration activation,
new defaults, schema changes, or deletion guarantees ship with this change.
The single Go binary, embedded React UI and PostgreSQL architecture is unchanged.

## Contract

`internal/privacy/policy.go` is a pure computation over value inputs. It has no
DB, network, environment, clock, or ingestion dependency. Evaluate snapshots at
configuration refresh and publish them through the existing in-memory site
registry when integration is approved; do not add DB reads to ingestion.

- `Mode` is typed: `Anonymous` is the safe zero value; `Identified` permits
  identity only when `Project.ProfilesEnabled` is also true. Effective identity
  is **instance permission AND project opt-in**.
- Replay is independent: `Instance.ReplayAllowed AND Project.ReplayEnabled`.
  Identified mode never enables replay. Anonymous mode can permit replay if both
  replay switches are explicitly enabled; anonymous identity permission is not
  a promise that recordings contain no personal data.
- `Decision` preserves both configured inputs, effective mode/replay/rules, and
  typed restriction reasons. Disabled gates each get a reason (including when
  both layers disable a feature). Cap reasons describe an instance rule that
  actually tightens a project value. Equal or stricter project values need no
  additional cap reason. Reasons are internal codes, not a published API.
- Replay masking combines with OR; sampling, retention and quota combine with
  minimum. A later layer cannot remove required masking or increase a cap.
  `ReplayRules.Tighten` is commutative, associative and idempotent for validated
  inputs. `Evaluate` validates both inputs, even when collection is disabled,
  and returns an error and zero decision for an invalid input.
- Rules use existing units: integer sample percentage (0–100), retention days
  (0–365 in this primitive), and nonnegative `int64` quota bytes. These are
  **concrete limits**: zero means zero, never unset or unlimited. Existing replay
  settings require positive retention/quota; this primitive additionally admits
  zero as a strict cap so omission cannot accidentally become unlimited.
  A future adapter must resolve absent values explicitly and suppress/reject
  collection when a zero storage cap cannot be honored. `ReplayEnabled` is only
  permission: sampling, storage capacity, consent and privacy signals can still
  prevent collection. Limits are computed even when permission is off.

`internal/config/tracking.go` proposes `PANEL_TRACKING_MODE`, matching existing
`PANEL_*` names. `ParseTrackingMode(raw, present)` accepts the pair from
`os.LookupEnv` without reading the environment itself. Exact `anonymous` and
`identified` are valid. Explicit empty values, whitespace, case variants and
unknown strings fail validation without echoing the input. An unset value returns
anonymous with `Explicit == false`; explicit anonymous has `Explicit == true`.
This distinction must survive rollout resolution. `config.Load` does not call
this parser, and the variable has **no runtime effect today**. Its current
`getenv func(string) string` interface cannot distinguish unset from empty;
coordinate a presence-aware integration rather than silently collapsing them.

## Mapping existing types

The policy types are projections, not another persisted settings schema:

| Existing source | Future policy input |
| --- | --- |
| Parsed tracking mode | `Instance.Mode` |
| `config.ReplayEnv.Enabled` | `Instance.ReplayAllowed` |
| `sites.Site.ProfilesEnabled` | `Project.ProfilesEnabled` |
| `sites.Site.Replay.Enabled` | `Project.ReplayEnabled` |
| `ReplayEnv.Mask/SampleRate/Retention/QuotaBytes` | Instance replay requirements/caps, after resolving nil |
| `sites.ReplaySiteInput.Mask/SampleRate/RetentionDays/QuotaBytes` | Project replay rules, after resolving nil |

Keep optional-pointer provenance at the adapter boundary. No defaults for those
nil values are selected here. `replay.SiteSettings` and its `Value.V/Locked`
representation already serve runtime/UI settings. Integrate configured/effective
values and reasons into that existing boundary later; do not replace the storage
model or infer privacy safety from `Locked`. Triggers, consent, storage backend,
raw-event retention classes and authorization grants are outside these primitives.

## Integration gaps — not fixed

Evidence below is from foundation commit
`91791f441c1114798655aac1b45f25e2e41ec8cd`; line numbers may move on integration.

- **Profiles are gated only by the site.** `internal/ingest/handler.go:100`
  calls `payload.Identity(site.ProfilesEnabled)`. The instance permission is not
  consulted. Future enforcement must cover identity fields, identify events,
  traits and all collection paths that can attach profile IDs.
- **Queued writes need enforcement too.** `internal/writer/writer.go:396`
  upserts identities already in queued events. The final-boundary tombstone
  check in `internal/writer/profile_tombstones.go:13` addresses erased identities,
  not a mode/project downgrade. Recheck current effective policy at persistence,
  including pending/retried batches and session links; do not trust admission-time
  permission indefinitely.
- **Historical reads deliberately remain available for erasure.**
  `internal/platform/http/handlers_profiles_test.go:389` documents and tests this.
  Collection permission must not replace the existing authorization checks or
  strand data that an authorized operator needs to inspect/delete.
- **Replay ignores project Enabled and can weaken stricter values.**
  `internal/replay/settings.go:83` uses only the environment enable switch;
  `resolve` in that file gives the environment precedence over the project.
  Thus environment mask=false or a larger numeric value can weaken a project
  choice. This foundation does not replace that resolver or its callers.
- **Heatmaps may bypass tracker consent.**
  `internal/tracker/tracker.extensions.js:163` sends through `p._post`, exposed at
  `internal/tracker/tracker.js:193`; the consent queue lives in `emit` at line 66.
  Visitor opt-out and consent are distinct checks. Browser and server integration
  tests must cover consent denial/revocation and direct submissions.
- **Arbitrary payloads can carry identity.** URLs, referrers and properties in
  `internal/ingest/payload.go:35` are bounded, not guaranteed identity-free.
  Anonymous mode only controls explicit identity permission. URL/property
  sanitation, allowlists and replay redaction require separate design/tests;
  do not advertise personal-data-free collection based on this mode alone.
- **Durable deletion and multi-instance downgrade remain design work.** Existing
  process-local tombstones are not a complete restart/multi-instance erasure
  protocol. Specify durable deletion jobs, idempotent retries, policy versions,
  cache invalidation, in-flight write ordering and cross-instance barriers before
  promising that a downgrade or deletion has completed everywhere.

## Rollout decisions requiring confirmation before activation

Proposed fresh installs: anonymous identity mode, profiles off, replay off.
These are proposals, not activated defaults. Decide how upgrades classify an
unset mode and existing opt-ins; do not silently infer identified permission or
silently destroy data. Invalid explicit configuration should fail startup once
wired. Upgrading to identified must preserve per-project opt-in and must not
turn on replay. Downgrading must stop new identity collection and account for
queued writes, retries, cached settings and all instances before acknowledging
completion. Replay permission changes remain independent. Historical authorized
reads and erasure remain available; neither a mode change nor a tighter retention
cap alone proves historical data has been deleted. Approve retention/deletion
semantics and the migration/operator communication plan before runtime wiring.

## Verification

The focused suite needs no database or frontend build:

```sh
go test -race -count=1 -cover ./internal/privacy ./internal/config
go test ./internal/privacy -run '^$' -fuzz FuzzReplayRulesTighten -fuzztime 5s
go vet ./internal/privacy ./internal/config
```

Tests cover all 16 identity/replay gate combinations, safe zero values, invalid
inputs, configured/effective preservation, restriction reasons, stricter project
rules, zero caps, parser presence and strict spelling, and an inactivity guard on
`config.Load`. Fuzzing checks monotonicity and the composition laws. These tests
prove the primitive contract; they do not prove runtime enforcement or rollout.
