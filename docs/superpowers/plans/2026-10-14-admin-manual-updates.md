# Admin-triggered manual updates: design and implementation plan

**Status:** accepted direction; implementation has started with the read-only update-availability slice.

**Goal:** An instance administrator can see when an official stable Supermetry release is available and explicitly install it from the Admin UI. Every update must take and validate a complete database backup first. Availability checks are read-only and cached; there is no automatic download or apply.

**Supported in v1:** Linux deployments created by the official installer: systemd binary installs and downloaded-image Docker Compose installs. Other binaries, local Docker builds, arbitrary Compose layouts, Kubernetes, macOS, and Windows show the current/latest version and manual instructions only.

## 1. Feasibility and existing foundations

This is feasible, but the web process must not update its host directly.

- Binary installs run as the unprivileged `supermetry` user with `ProtectSystem=strict`; they cannot replace `/usr/local/bin/supermetry` or restart systemd (`internal/installer/templates/supermetry.service:6-23`).
- The Docker image runs as UID 65532 and deliberately has no Docker socket (`deploy/Dockerfile:23-43`). Mounting the host Docker socket into the web container is forbidden because it is effectively root access to the host.
- Releases already build versioned OS/architecture archives and `SHA256SUMS` (`scripts/release.sh:7-95`), and the installer already verifies the selected checksum (`deploy/install.sh:65-118`).
- `supermetry backup` already takes a whole application-database custom-format dump and writes a version/schema manifest atomically with mode 0600 (`cmd/supermetry/backup.go:149-244,443-490`). It excludes the secret key, GeoIP files, replay blobs, and PostgreSQL globals; the UI must call this a **complete database backup**, not a complete instance backup.
- Instance-admin authorization, CSRF protection, rate limiting, audit rows, and the running version are already present (`internal/platform/http/handlers_admin.go:21-45`, `internal/platform/http/private.go:210-222`, `internal/platform/store/audit.go:50-65`, `internal/platform/http/handlers_account.go:108-140`).

## 2. Chosen architecture

Use a narrow, root-owned host helper activated through a Unix socket. The helper may be implemented as a hidden `supermetry updater-helper` subcommand, but it is a separate process and systemd unit from the web server.

```text
Admin browser
    -> cookie + CSRF + recent password/2FA
Supermetry web process (unprivileged)
    -> exact-version request over /run/supermetry-updater/control.sock
Root-owned updater helper
    -> official GitHub release only
    -> verified database backup
    -> versioned binary slot OR pinned GHCR image digest
    -> restart + /readyz + reported-version check
```

The helper accepts only a job ID, target stable semantic version, and one-time nonce. It obtains fixed deployment facts from `/etc/supermetry/deployment.json`: mode, service name, binary slots or Compose directory, backup directory, local readiness URL, and updater socket group. It never accepts a URL, repository, executable path, Compose directory, image name, shell fragment, or checksum from the web application.

For installer-managed Docker, mount only the narrow updater socket into the app container and add the socket group GID. The host helper alone may use Docker. Generic Docker deployments remain manual.

### Explicitly rejected approaches

- Replacing the executable from the web process: blocked by the existing sandbox and unsafe if the app is compromised.
- Mounting `/var/run/docker.sock` into the app container: grants host-root-equivalent control.
- Re-running `supermetry install`: it is an installer, not a transactional updater; today it can regenerate configuration and does not reliably restart an already-running binary.
- Watchtower or any tag-watching service: violates the no-auto-update requirement and cannot enforce the mandatory backup gate.
- Trusting `SHA256SUMS` from the same release as the archive: detects corruption, but not malicious replacement of both files.

## 3. User-visible behavior

Admin > System update shows the running version, managed deployment type, last update result, and whether UI updates are supported. For instance admins, the persistent app shell makes a cached read-only availability check and shows a small **Update available** control in the top-right header when a newer stable release exists. Non-admins never call the endpoint or see the control.

1. The admin shell asks for update status on load. The server contacts the fixed GitHub latest-release endpoint at most once per six-hour cache window; the client does not refetch on window focus. Pre-releases are ignored in v1. A **Check again** action may explicitly bypass the cache later.
2. If a newer version exists, show its version, published date, release-notes link, database-backup warning, expected downtime, and available disk-space result.
3. **Update to X** opens a confirmation dialog. Require the administrator's current password and TOTP when enabled, plus exact target-version confirmation.
4. The progress view shows `preflight -> download/pull -> verify -> stop/drain -> backup -> activate -> restart -> verify -> complete`.
5. The web server will disappear during the update. The page treats connection loss as expected, polls `/readyz`, then reloads `/api/private/me` and requires the reported version to equal the target.
6. A backup failure aborts before installation and restarts the old version. Failures after a schema change show a recovery-required state and exact operator instructions; they never blindly start the old binary against a newer schema.

No server timer, startup check, automatic download, unattended install, automatic apply, or silent retry is added. The only network check is caused by an instance admin loading the signed-in app or explicitly checking again.

## 4. Release trust and packaging prerequisite

The repository is currently private and has no GitHub Release. Do not enable UI updates until the repository is public and at least two signed test releases exist; clients must never require a maintainer's private GitHub token.

Create a canonical `release-manifest.json` containing:

- release version, source commit, publication time, stable/prerelease flag;
- each binary archive's OS, architecture, size, and SHA-256;
- the immutable multi-architecture GHCR digest and per-platform digests;
- minimum source version, target schema version, and rollback-compatibility metadata.

Sign the canonical bytes with Ed25519 and publish `release-manifest.json.sig`. Embed a small keyring of trusted public keys in the helper, document rotation, and keep the signing key in a protected GitHub release environment with manual approval. Keep `SHA256SUMS` for human use. After the repository is public, also generate GitHub artifact attestations as defense in depth; verification of the embedded-key signature remains self-contained and does not require `gh` on customer hosts.

Before this feature, fix and test these current packaging gaps:

- set `file: deploy/Dockerfile` in the GHCR build;
- pass `VERSION=<tag>` to the Docker build so the container never reports `dev`;
- align the installer Compose data mount with the image's `SUPERMETRY_DATA_DIR`;
- make backup use the same resolved database configuration as `serve`, including split Docker variables;
- pin installed Docker deployments to the signed image digest, never `latest`;
- enable immutable GitHub releases when the repository becomes public.

## 5. Durable state and API

Add `instance_update_jobs` in a forward-only transactional migration. Use a bigint identity key and text status with a CHECK constraint; do not add an extension or PostgreSQL enum. Store requester, from/to versions, deployment mode, signed-manifest digest, status, timestamps, backup SHA-256/size, schema before/after, and a redacted error code/message. Never store the database URL, release credentials, password/TOTP, or the one-time nonce. Enforce one active job with a partial unique index over active statuses.

The helper keeps its authoritative crash-safe state under `/var/lib/supermetry-updater/jobs/`, written using same-directory temp files, fsync, and rename. The application reconciles that state into `instance_update_jobs` and instance audit entries after restart or status polling.

Private instance-admin API:

- `GET /api/private/admin/update` — local capability, current version, cached check, and last/current job; no network.
- `POST /api/private/admin/update/check` — explicit official-release check.
- `POST /api/private/admin/update/apply` — exact version, recent password/TOTP proof, and idempotency key; returns 202.
- `POST /api/private/admin/update/cancel` — only before stop/backup begins; optional for v1 if it delays the core path.

Every handler uses `adminPrincipal`; apply rechecks instance-admin status inside the job-creation transaction. Add route-matrix tests and audit `instance.update.requested`, `.backup_completed`, `.succeeded`, `.failed`, and `.recovery_required`. Audit only actor, versions, deployment type, signed digest, backup digest/size, and failure class.

## 6. Update transaction

### Common preflight

1. Acquire both the database single-active-job constraint and a host `flock`; reject a second update with 409.
2. Reject `dev`, prerelease, same-version, downgrade, unsupported platform, unsupported install, local-build Docker, or malformed semver targets.
3. Check the fixed GitHub repository, bounded redirects/response sizes/timeouts, disk headroom for download + database backup + old/new artifacts, database reachability, backup tool availability, and writable fixed directories.
4. Download the manifest/signature first and verify Ed25519 before trusting any URLs, sizes, hashes, schema metadata, or image digests.
5. Download into the updater staging directory while hashing and enforcing the declared size. Reject archives with unexpected paths, links, devices, duplicate entries, or decompression excess. Validate the staged binary's version/platform only after signature verification and inside a restricted subprocess.

### Mandatory backup gate

1. Gracefully stop the application, allowing the current SIGTERM drain/flush path to finish. Keep PostgreSQL running.
2. Run the **old** release's backup command so the backup format matches the pre-update schema:
   - binary: invoke the old versioned binary against its existing environment;
   - Docker: run a one-off old-image Compose command with the same environment and backup bind mount.
3. Write to the installer-managed host backup directory, mode 0600, with no browser download. Validate archive structure/manifest and run the existing non-destructive `pg_restore --list`/dry-run checks. Hash it and record size, path in helper state, and schema version.
4. If any backup or validation step fails, do not mutate the installed artifact; restart the old service and mark the job failed.

Backup retention is not silently invented. Keep update backups until the operator deletes them; add a separately approved retention policy later.

### Binary activation

- Store releases at `/opt/supermetry/versions/<version>/supermetry`, root-owned and non-writable by the app.
- Keep `previous` and `current` symlinks and make systemd execute `current/supermetry`.
- Stage, fsync file and directory, atomically switch `current`, restart systemd, wait on `/readyz`, then verify `/api/private/me` or `supermetry version` equals the target.
- Preserve at least the previous slot. If readiness fails and schema did not change, atomically switch back and restart. If schema advanced, stop and enter recovery-required instead of attempting binary-only rollback.

### Docker activation

- Pull `ghcr.io/velixio/supermetry@sha256:<signed digest>` before downtime.
- Preserve the previous Compose file and image digest. Atomically update only the Supermetry service image; never run `down`, remove volumes, or recreate the bundled PostgreSQL service.
- Run `docker compose up -d --no-deps supermetry`, wait for health/readiness, and verify the reported version.
- If readiness fails and schema did not change, restore the previous digest and recreate only the app service. If schema advanced, enter recovery-required.

The current in-place restore path is not an automatic rollback mechanism. A later feature may restore into a fresh database and atomically switch databases where permissions allow; v1 provides a tested manual recovery runbook.

## 7. Implementation tasks and commit boundaries

Each task is test-first. Keep commits small and layer-oriented; do not combine the privileged helper, API, and UI in one commit.

### Task 0 — Release/installer blockers

**Modify:** `.github/workflows/release.yml`, `deploy/Dockerfile`, `internal/installer/{compose.go,envfile.go,install.go}`, templates and tests.

- Add failing release-workflow static tests for Dockerfile path, version build arg, immutable digest output, and required manifest assets.
- Fix Docker data path and version reporting; preserve existing env on installer reruns.
- Add installer-managed deployment manifest rendering with strict paths/modes for binary and Docker installs.
- Acceptance: a locally built tagged binary and image both report the tag; Docker data survives app recreation; rerun preserves custom config.

### Task 1 — Reusable, verifiable database backup

**Refactor:** `cmd/supermetry/backup.go` into an internal package without changing CLI behavior.

- Resolve database settings through normal config, support an explicit fixed backup directory, and expose `Create` and `Verify` APIs.
- Add free-space checks, archive hash/size result, cancellation, sanitized errors, and Docker-variable tests.
- Acceptance: unit tests plus backup/restore into a fresh disposable PostgreSQL database; failure leaves no final or partial archive.

### Task 2 — Signed release manifest and release client

**Create:** updater manifest/parser/verifier and bounded official-GitHub client packages.

- Test canonical encoding, Ed25519 keyring/rotation, signature and digest mismatch, wrong platform/version, prerelease/downgrade, redirects, timeouts, oversized data, archive traversal/symlinks/bombs, and cancellation.
- Restructure the release workflow so binary hashes and OCI digests are known before the signed release is published.
- Acceptance: two local test releases and one intentionally corrupted fixture; only the valid target installs.

### Task 3 — Helper protocol, state machine, and deployment backends

**Create:** privileged helper command/package, length-prefixed versioned Unix-socket protocol, atomic job store, fixed deployment descriptor, and runner interfaces.

- State transitions are explicit and monotonic; every transition is fsynced. On reboot, resume only safe pre-activation work and classify ambiguous activation as recovery-required.
- Fake all command execution in unit tests. No shell strings; use fixed executables and argument arrays.
- Add fault injection after every persistent transition and test power-loss recovery.

### Task 4 — Binary/systemd installation and update backend

**Modify:** installer service templates; add updater `.socket` and `.service` units and versioned binary layout.

- Install with the least systemd permissions practical; socket accessible only to the app/updater group.
- Test atomic slot switching, mode/owner preservation, graceful stop, backup-before-switch, expected-version health check, unchanged-schema rollback, and changed-schema refusal.
- Acceptance: disposable Debian VM update from release N to N+1 and failure rollback, with the database backup restored successfully into a fresh database.

### Task 5 — Installer-managed Docker backend

**Modify:** Compose template and installer; never mount the Docker socket.

- Mount only the updater control socket and a host-visible backup directory; add the numeric socket group safely.
- Pin by signed digest, update only the app service, preserve database and app volumes, and validate reported version.
- Acceptance: real Compose N -> N+1 test, forced unhealthy N+1 rollback with unchanged schema, and proof that the app container has no Docker socket.

### Task 6 — Database model, admin service, API, and audit

**Modify:** migrations, `internal/platform/admin`, HTTP routes/tests, OpenAPI, and generated client.

- Write concurrency tests first for the one-active-job invariant and revoked-admin race.
- Add recent password/TOTP verification, CSRF, idempotency, strict JSON, redacted failures, helper-unavailable response, and reconciliation after restart.
- Acceptance: non-admin and org-admin 403; revoked admin 403; stale auth rejected; concurrent clicks produce one job; completion/failure audits contain no secrets.

### Task 7 — Admin UI

**Create:** `/app/admin/update`, update hooks/components, localized strings, and route/sidebar entry.

- Render unsupported/manual deployments clearly; never imply Docker is updatable unless the host helper is registered.
- Add Check button, release notes link, backup scope warning, reauthentication/typed confirmation dialog, progress/reconnect view, and recovery instructions.
- Tests cover accessibility, non-admin redirect, no network-on-page-open, expected disconnect, reload/resume, helper unavailable, failure, success, and mobile layout.

### Task 8 — Documentation, threat model, and recovery drill

**Modify:** install/deploy docs, security threat model, release checklist, changelog.

- Document exact backup contents/exclusions, backup location/permissions, release key rotation, supported deployment matrix, manual-update commands, and schema-changed recovery.
- Run adversarial review for helper privilege escalation, socket spoofing, symlink races, compromised GitHub release/workflow, downgrade/replay, disk-full, partial migration, readiness false positives, and backup disclosure.

### Proposed PR and commit split

- **PR A — release and backup foundation (5 commits):** Docker release fixes; deployment manifest; backup package refactor; backup verification/headroom; signed release-manifest workflow.
- **PR B — privileged binary updater (6 commits):** protocol/state model; bounded release client; systemd socket/helper install; binary slots; health/rollback; fault-injection tests.
- **PR C — Docker updater (4 commits):** safe socket/group mount; digest-pinned Compose rewrite; Docker backup/activation; real Compose failure/recovery tests.
- **PR D — product surface (6 commits):** update-job migration; admin service/audit; HTTP/OpenAPI/client; Admin UI; browser tests; docs/threat model/recovery drill.

Use `scripts/split-commits.py` for each implementation batch and require every intermediate commit to build and pass its focused tests. Push only after the corresponding review and gate pass.

## 8. Final release gate

- Full Go race suite, vet, formatting, web typecheck/tests/build, route authorization matrix, and browser QA pass.
- Clean-machine binary and Docker installer tests pass before any UI-update test.
- Real N -> N+1 and failed-N+1 tests pass for systemd and installer-managed Compose.
- A backup from each mode restores into a fresh PostgreSQL database and starts release N successfully.
- Kill/power-loss fault tests cover download, backup, slot/Compose switch, restart, and health verification.
- Released archive and image match the signed manifest and report the exact tag.
- Neither app process can write its installed binary/Compose file or access Docker; only the narrow helper can.
- Only an authenticated instance administrator loading the signed-in app, or explicitly checking again, can cause an update-related network request; the result is cached for six hours.

## 9. Product decisions still needed before implementation

1. **Backup retention:** keep forever (safe default for v1) or offer an explicit count/age policy later.
2. **Downgrades:** recommended v1 policy is never through the UI; manual recovery only.
3. **Channels:** recommended v1 policy is stable releases only; beta/nightly can be a later opt-in.
4. **Unsupported platforms:** recommended behavior is check + copyable manual instructions, never a disabled button with no explanation.

Unless changed by the user, implementation should use the recommended v1 choices above.
