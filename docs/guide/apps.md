# App Monitoring

App Monitoring shows when apps break, why, and who is affected: grouped issues,
crash-free users and sessions, releases and compatibility. Desktop, mobile and
backend apps can report to it with an unmodified **Sentry SDK** (change only the
DSN), with the Supermetry client kits, or over plain HTTP.

## Sentry SDKs: change the DSN

Create an app project (Sites, New, App). **Settings, Installation** shows your
DSN, which looks like `https://<public key>@<your host>/<project id>`, with a
copy button and a snippet per SDK: JavaScript (browser), Node, Electron, Python,
Go, Android and Cocoa. Every snippet sets only two options, `dsn` and `release`.
Keep the SDK's default settings; its automatic capture of errors, breadcrumbs
and sessions works as is.

```js
Sentry.init({
  dsn: "https://<public key>@<your host>/<project id>",
  release: "my-app@1.0.0",
});
```

**Sentry SDKs send sessions only when `release` is set in `Sentry.init`. Without it, crash-free rates fall back to error events.**

The Releases page shows adoption, crash-free sessions and users and session counts per release. Click a release to see its daily crash-free sessions, the session status split (healthy, errored, abnormal, crashed) and handled versus unhandled errors. Abnormal sessions (ANR, app hang) are not counted as crashes. Releases without tracked sessions are marked "from error events".

The DSN public key is meant to ship inside client bundles, so it is shown in
full. Under **DSN keys** you can create more keys (for example one per
environment), give each a label, restrict a key to allowed origins (browser
requests from other origins are refused), and revoke a key at once. A key
belongs to one app project.

An issue lists its latest events. **Full event detail** shows the exception
chain with in-app frames highlighted, breadcrumbs, contexts (OS, device, app,
runtime, browser), tags and attachments. Screenshots (PNG, JPEG, WebP) show as
thumbnails and text or JSON files open in a viewer, both with a download link;
text is scrubbed like the rest of the event. Files over 1 MiB, anything past 5 MiB
per event and other file types keep their name and size only. Event detail and
attachments are kept for 30 days; the issue and its counts are kept longer. Messages, emails, tokens, IP addresses,
cookies and query strings are scrubbed before storage. See
[Migrate from Sentry](migrate-from-sentry.md) for what is and is not supported.

The sections below describe Supermetry's own app endpoint and client kits, still
supported and listed under **Legacy kits** on the Installation page.

## Electron apps and native crashes

An Electron app using `@sentry/electron` needs only the DSN changed. JavaScript errors in the main and renderer processes arrive as events; a **native crash** (a crash of Electron itself, such as `process.crash()` or a segfault in a native module) is saved by Crashpad as a minidump and sent by the SDK on the next start. All of them land in the same app project, each in its own issue, and every event carries a `process` tag (`main`, `renderer`, `gpu`, `utility` or `other`). The issue list has a **Process** filter (the search term `process:renderer` does the same), and an issue's detail page has a **Process** breakdown. Issues are never merged across processes.

```js
const Sentry = require("@sentry/electron/main");
Sentry.init({ dsn: "https://<public key>@<your host>/<project id>", release: "my-app@1.0.0" });
```

If your app drops events in `beforeSend` (for example until the user consents), the SDK routes native crashes through the same hook, so a `beforeSend` that returns `null` sends no crash report either.

Other Crashpad clients can post a dump straight to `POST /api/<project id>/minidump/` as `multipart/form-data` (field `upload_file_minidump`, plus optional `sentry[release]`, `sentry[environment]`, `sentry[dist]`, `sentry[user][id]`, `sentry[tags][<key>]` and Crashpad's `prod`, `ver`, `ptype` and `platform`; every other field is dropped). The DSN public key goes in the `sentry_key` query parameter or an `X-Sentry-Auth` header. A dump may be gzip-compressed and is limited to 16 MiB after decompression (larger is a 413).

**What is stored from a minidump.** A minidump holds raw process memory, so it is read in memory and thrown away; it is never written to the database, the attachment store or a log. Supermetry keeps only what it derives from it: the crash reason and address, the crashing thread's frames (function, source file and line, module file name and offset), and the modules' file names (paths are scrubbed) with their debug ids. Other threads, registers, stack contents and environment variables are not kept. Because the dump is not kept, a crash that arrives before its symbols are available stays unsymbolicated (frames show as `module+0xoffset`) and the issue is regrouped only for new events once the symbols are uploaded. The same module and offset always group into the same issue.

**Symbols.** Frames are resolved with Breakpad `.sym` files for amd64 and arm64 dumps (Linux, macOS and Windows x64):

- **Electron's own binaries** are looked up on Electron's public symbol server (`https://symbols.electronjs.org`, by module file name and debug id) the first time a crash needs them, converted, and cached for every project on the server. Only the module file name and the debug id of Electron's own binaries leave the server; nothing else, and never the dump. A symbol file for the Electron binary is large (about 0.75 GB for Linux x64, about 0.45 GB once converted), and fetching it takes longer than a crash is allowed to wait: the first crash of a new Electron version is stored without symbols (frames as `electron+0xoffset`) while the server fetches in the background, and later crashes of that version are symbolicated. Turn it off for the whole server with `SUPERMETRY_ELECTRON_SYMBOLS=false`, or per project with **Fetch Electron's public symbols** under **Native crashes** in the project's app settings (on by default). `SUPERMETRY_REMOTE_SYMBOLS_MB` (default 4096) caps the on-disk cache of fetched symbols, evicting the least recently used; a module the server does not have is not asked for again for 24 hours.
- **Your own native modules**: generate a `.sym` file with Breakpad's `dump_syms` and upload it with `sentry-cli debug-files upload`. Your own uploads are always used before the public server and are counted in the organization's storage like other debug files.
- **Windows**: modules are matched by their PDB id. Upload `.sym` files made with `dump_syms` (reading `.pdb` files directly is not supported yet), or rely on Electron's public symbols for the Electron binaries. Frames of Windows x64 modules are unwound from the `STACK CFI` records of the `.sym` file, then by frame pointer, then by scanning the stack.

`SUPERMETRY_MINIDUMP_INFLIGHT_MB` (default 128) bounds the minidump bytes waiting for or in processing; a dump over the budget is dropped with the event kept and counted as `minidump_skipped` on the **Data health** page. x86 and 32-bit ARM dumps keep their modules and crash reason, but only the crashing frame is shown.

## Sending reports from desktop and mobile apps

Desktop apps (Electron, Tauri, native), mobile apps and backend services can send
events, including failure reports, to the public events endpoint that the web
tracker uses. This guide lists the rules that decide whether a report is stored.
Breaking most of them gives no error back: the request still gets `202 Accepted`,
and the report is only counted on the **Data health** page.

New integrations should use the dedicated app endpoint below: an app project
with an ingest key, per-event drop reasons in the reply, and crash grouping into
issues. The older web events endpoint, described further down, keeps working. A
web site can also be linked to an app project, so a client that already sends
`diagnostic_report` events needs no change.

## The app events endpoint

Create an app project (Sites, New, App) and copy its ingest key. The key looks
like `smk_` followed by 39 lowercase letters and digits. It is write-only and safe
to ship inside an app. It is shown only once; if you lose it, rotate it in
**Settings, App**. Rotating it keeps the old key valid for 7 days.

Every request needs both the app ID (the `site` field) and the key, and the key
must belong to that app. **Settings, Installation** shows the app ID and host
with setup code for each client kit, and waits for the first report.

```
POST /api/public/v1/app-events
Content-Type: application/json
X-Supermetry-Key: smk_...
```

```json
{
  "site": "<app project ID>",
  "events": [
    {
      "id": "evt-3f2a9c0e",
      "ts": 1763200000000,
      "kind": "crash",
      "release": "1.2.3",
      "platform": "macos",
      "arch": "arm64",
      "error_type": "TypeError",
      "frames": "src/main.ts:42 start\nsrc/app.ts:7 run",
      "install_id": "install_0123456789abcdef",
      "tags": { "stage": "startup" }
    }
  ]
}
```

A request carries 1 to 20 events and at most 16 KB. Browsers may call it from
any origin; the key is the trust signal, so there is no bot filter and no
allowed-hosts check, and a custom User-Agent is not required.

### Event fields

| Field | Type | Notes |
|---|---|---|
| `id` | string, 1 to 64 | Required. Dedup key; a repeat within 15 minutes is dropped as `duplicate`. |
| `kind` | enum | Required: `crash`, `error`, `hang`, `install_failed`, `install_stalled`, `incompatibility`, `operation_failed`, `session`, plus the product kinds `screen` and `event` (see [Product analytics](#product-analytics-for-apps)). |
| `ts` | integer, ms | Time the event happened. Optional (defaults to receive time). More than 5 minutes ahead is clamped to receive time. |
| `level` | enum | `fatal`, `error`, `warning`. Defaults to `fatal` for a crash and `error` otherwise. |
| `handled` | boolean | |
| `release`, `build` | string, up to 64 | App version and commit or build number. |
| `environment` | string, up to 32 | Stored as `(other)` when the project lists environments and this isn't one. |
| `platform` | enum | `windows`, `macos`, `linux`, `ios`, `android`, `web`, `other`. |
| `arch` | enum | `x64`, `arm64`, `x86`, `arm`, `other`. |
| `os_version`, `device_class`, `runtime` | string, up to 32 | Keep `os_version` to major.minor. |
| `error_type` | string, up to 64 | The exception class, never its message. |
| `exit_reason`, `signal` | string, up to 32 | Letters, digits and `_ . : -` only. |
| `exit_code` | integer | |
| `frames` | string | Newline-separated `path:line function`. Scrubbed on arrival (see below), cut to 4 KB. |
| `fingerprint` | hex, up to 64 | Optional grouping key. Computed from the kind, error type and top 5 frames when absent. |
| `occurrences` | integer, 1 to 1,000,000 | For clients that coalesce repeats. |
| `install_id`, `session_id` | string, up to 128 and 64 | Pseudonymous ids for crash-free users and sessions. Stored only as an HMAC with a per-app secret. |
| `tags` | object | Up to 20 keys; string, number or boolean values; keys up to 64 characters, values up to 200 bytes. |
| `screen` | string, 1 to 200 bytes | `screen` kind only, required: the screen name. Shown as `/name` in funnels and journeys. |
| `name` | string, 1 to 64 | `event` kind only, required: the custom event name. Letters, digits and `_ . : $ - ` and space; must not start with `$`. |
| `props` | object | `screen` and `event` kinds: up to 20 string values; keys up to 64 characters, not starting with `$`; values up to 200 bytes. |

A wrong type, an over-long value or an unknown enum value drops that event as
`invalid`. Unknown fields are ignored.

### The reply

Problems with the request as a whole fail it: `400` for a bad method, a body over
16 KB or bad JSON, `401` for an unknown app, a project that is not an app, or a
missing, malformed, wrong or expired key (every cause gives the same response),
and `429` when one client address sends more than 60 requests a minute.

Otherwise the reply is `202`, and says what happened to each event:

```json
{
  "accepted": 1,
  "dropped": [{ "index": 1, "reason": "too_old" }],
  "warnings": [{ "index": 0, "reason": "tag_not_allowed", "tag": "token" }]
}
```

Drop reasons: `too_old` (older than the project's max report age or retention,
whichever is shorter), `invalid`, `rate_limited` (more than 600 events a minute
for the key, or the intake queue is full), and `duplicate`. If the project has a
tag allowlist, other tags are removed, the event is kept, and a warning names
each tag. At most 1,000 distinct values per tag key are kept per day; later ones
are stored as `(other)`. Every drop also appears on the **Data health** page.

### What is scrubbed

Stack frames are scrubbed before anything is stored: home folders
(`/Users/<name>/`, `/home/<name>/`, `C:\Users\<name>\`) become `~/`, any other
absolute path keeps only its last three segments, and `file://` prefixes are
removed. Raw `install_id` and `session_id` values are never stored.

### Inbound filters

Events that are noise are dropped before anything is stored. They are counted as
`filtered` and still get a `200` reply, so SDKs do not retry. Each filter is a
switch under **Settings, Inbound filters** of the app project (owner or admin):

| Filter | What it drops | Default |
|---|---|---|
| Browser extensions | Events with a stack frame from a `chrome-extension://`, `moz-extension://` or `safari-extension://` file | On |
| Crawlers | Events sent with a crawler or bot User-Agent | On |
| Localhost | Events whose request URL is `localhost`, a `.localhost` name or a loopback address | The server's `SUPERMETRY_SENTRY_FILTER_LOCALHOST` (off unless set) |

The localhost switch follows the server-wide setting until you change it for a
project. The settings API (`PUT /api/private/sites/{site}/app-settings`) takes
`filter_extensions`, `filter_crawlers` and `filter_localhost`; `null` returns a
switch to its default.

### Erasing a user's data

To honour an erasure request for one end user, use **Settings, Erase a user's
data** of the app project (owner or admin). The dashboard calls the private API
with your signed-in session:

```text
POST /api/private/sites/<site-id>/app/users/erase
{"user_id": "the-id-your-sdk-reports"}

202 {"id": 41, "status": "pending", "status_url": "/api/private/sites/<site-id>/app/users/erasures/41", ...}
```

The erasure runs in the background, in batches, so the call replies at once.
Poll the status URL (`GET`, owner or admin) until `status` is `done`; it then
carries `bodies_deleted` and `events_anonymized`. The settings page does this
for you. The work resumes by itself after a restart.

The user is matched by a keyed hash of `user_id`, so the id itself is never
stored, and it is not written to the audit log either; only the counts are.

Erased:

- the stored event bodies (the scrubbed event detail) of the user's events;
- the user hash on the user's event rows, which can no longer be tied to them.

Not erased:

- the event rows themselves and the aggregate counts built from them (issue,
  release and session counts). They hold no user identifier once the hash is
  cleared, so they are anonymous statistics.

Asking again for a user whose erasure is still queued or running returns the
same request. Once it is done, a repeat is a new request that reports zeros.
The audit entry is written when the erasure finishes.

### Sending diagnostic reports through a web site

A web site can be linked to an app project in **Settings, General, Send
diagnostic reports to app** (owner or admin). After that, a `diagnostic_report`
event sent to the web events endpoint is stored in the app project, with the
app's max report age (default 7 days) instead of 24 hours, and not as a custom
event. The site's allowed hosts, strict origin, bot and duplicate checks still
apply, and profiles must be on. An unlinked site stores it as a custom event, as
before.

## Endpoint of the web events API

```
POST /api/public/v1/events
Content-Type: application/json
```

```json
{
  "site": "<site ID>",
  "name": "diagnostic_report",
  "url": "https://app.example.com/diagnostics",
  "id": "3f2a9c0e8b7d4e6fa1c2d3e4f5a6b7c8",
  "ts": 1763200000000,
  "profile_id": "install_0123456789abcdef",
  "props": {
    "category": "app_crash",
    "app_version": "1.2.3",
    "platform": "macos",
    "arch": "arm64",
    "error_type": "TypeError"
  }
}
```

Limits: a body of at most 8 KB, at most 30 props, prop keys of at most 300
bytes and values of at most 2000 bytes.

## Rules

1. **Send your own User-Agent**, for example `MyApp/1.2.3 (macos; arm64)`.
   Headless browsers and well-known crawler and HTTP-client agents are treated
   as bots and dropped. That includes Electron's default agent, which contains
   `Electron/`, and empty agents. In Electron, set the agent on the request, or
   send reports from the main process with a custom header.
2. **Add the reporting host to the site's allowed hosts.** The host in `url`
   must be listed under **Allowed hosts** in the site settings. If it isn't,
   the request is rejected with `400`.
3. **Turn profiles on if you need props.** In anonymous tracking mode, or with
   profiles off for the site, props are removed before storing. The report then
   arrives without its details.
4. **Give each report a stable `id`.** The id is 1 to 64 characters of
   `A–Z a–z 0–9 _ -`. If a retry sends the same id, the copy is ignored.
5. **Send queued reports within 24 hours.** `ts` is the time the event
   happened, in Unix milliseconds. Reports more than 24 hours old are dropped
   and appear on the **Data health** page as **Too old**. A timestamp more than five
   minutes in the future is replaced by the time the report was received, and
   counted as clock skew.
6. **Keep personal data out.** Don't send user names, email addresses or file
   paths that contain home folders. Strip them from stack frames before
   sending.

## Native and mobile symbols

Android, iOS, macOS and Linux crashes show readable function names, files and
lines once the matching debug files are uploaded. Upload them with the standard
Sentry tools; only the server address and token change.

1. In the project's **Settings, Installation**, create an **upload token** (see
   [Upload source maps](source-maps.md)). It is the only secret you set.
2. Upload with the tool you already use:
   - **Android Gradle plugin**: in `build.gradle`, set `sentry { url = "https://<your host>/" }` and
     provide the token as `SENTRY_AUTH_TOKEN` (or `authToken`). The plugin uploads the ProGuard/R8
     mapping and, when enabled, the native libraries' symbols.
   - **Xcode**: the build phase of the Sentry iOS guide runs `sentry-cli debug-files upload`; set
     `SENTRY_URL`, `SENTRY_AUTH_TOKEN` and `SENTRY_PROJECT` in the phase's environment.
   - **By hand**: `sentry-cli debug-files upload <dir>` for dSYM bundles, Mach-O and ELF files
     (shared libraries and executables with DWARF or a symbol table), and
     `sentry-cli upload-proguard --uuid <uuid> mapping.txt` for ProGuard/R8 mappings.
3. Open **Settings, Symbols** to see every uploaded file and its state, and the
   debug files that events asked for and nobody uploaded.

Events are symbolicated as they arrive, so a file uploaded later does not rewrite
events that are already stored. An event whose symbols are missing shows a banner
naming the missing file (for example "Missing debug file for libfixture.so") with
a link to **Symbols** and to this guide; the same list is on that settings page.
System libraries (`/system`, `/usr/lib`, `/apex`, `libc.so`) are never listed:
nobody uploads symbols for them.

What is supported:

| File | Used for |
|---|---|
| ProGuard / R8 `mapping.txt` | Class, method and line names of Java and Kotlin frames, and the exception class. Matched by the mapping UUID the app reports. |
| Mach-O (thin or fat) with DWARF, dSYM bundles | iOS and macOS frames. A fat file serves every architecture; the event's own slice is used. |
| ELF with DWARF or a symbol table | Android NDK and Linux frames. The GNU build ID is the match key. |

Stored but not used for symbolication: bitcode symbol maps, PE/PDB, WASM and
source bundles (`--include-sources`). Uploads of these succeed so the tool never
fails a build. Split DWARF (`.dwo`, `.dwp`) gives function names only, without
lines. Swift and C++ names are shown as the debug info spells them: there is no
demangler yet.

Limits: each file is capped by `SUPERMETRY_DIF_MAX_MB` (default 512, the whole
file is parsed in memory while its cache is built, one file at a time), and
uploaded files count toward the organization's storage cap. The converted caches
live on disk under the data directory, bounded by `SUPERMETRY_SYMCACHE_DISK_MB`.

Out-of-memory kills and watchdog terminations are listed as their own issue kinds
(`oom`, `watchdog`), and the project overview breaks events and crash-free rates
down by platform.

`make sdk-compat-debugfiles` replays the command lines of the Gradle plugin and of
the Xcode build phase with `sentry-cli` against a real server and checks that each
file is listed with a usable cache.

## Checking delivery

The **Data health** page lists every dropped event by reason: bot user agent, host
not allowed, too old, clock skew, rate limited, and so on. If reports go
missing, look there first.

## Working on an issue

Open an issue to triage it. Owners, admins and editors can change an issue; viewers read it.

- **Assignee:** pick any member of your organisation who has access to the project, or unassign. Assignment is to people; teams are not supported.
- **Priority:** high, medium (the default) or low.
- **Status:** resolve, resolve in the next release, ignore or reopen, as before. New events from a later release regress a resolved issue.
- **Snooze:** ignores the issue until a time passes, or until it has had a number of more events or affected users. Reopening an issue ends the snooze.
- **Archive until escalating:** removes the issue from the open list and keeps it out of the way. Unarchive it to return it to open, or let Supermetry do it: see below.
- **GitHub link:** the API can link an existing GitHub issue (`https://github.com/<owner>/<repo>/issues/<number>`). Supermetry only stores the link and never calls GitHub.
- **Comments:** up to 10,000 characters each. You can edit or delete your own comments; owners and admins can edit or delete anyone's. Comments are visible to everyone in the project, so keep secrets and personal data out of them.
- **Activity:** a timeline of every change (status, assignee, priority, snooze, archive, comments) plus system rows for the first sighting and for regressions. It records the member who acted, or "System".

Through the private API, `PATCH /api/private/sites/{site}/app/issues/{issue}` takes any of `status`, `resolved_in_release`, `assignee` (a user id, `0` to unassign), `priority`, `snooze` (`until`, `events`, `users`), `archive` and `github_url`. Comments live under `.../issues/{issue}/comments` and the timeline at `.../issues/{issue}/activity`. A change that alters nothing writes no activity row.

### Snooze, archive and wake-ups

A job checks once a minute. A snoozed issue returns to open (activity "status", by System) when its time passes, when it has had the chosen number of more events since the snooze began, or when it has affected the chosen number of more users. Users are counted from events that carry a user; events without a user never count towards "N more users", so an issue snoozed until 100 users does not wake on anonymous events. An issue that someone resolved or reopened in the meantime is left alone.

An archived issue escalates back to open (activity "escalated", by System) when it had more than 10 events in the last hour and more than 5 times its hourly average over the 7 days before that hour. A steady issue that runs at 5 times its average but never passes 10 events an hour stays archived. The thresholds are fixed for now.

### Email notifications

Supermetry emails the person an issue is assigned to when someone else assigns it to them, when an issue assigned to them regresses, and (off by default) when someone else resolves it. You never get an email about your own change. Choose what you receive per project under Account, Notifications; assigned and regressed are on by default. Only members who can read the project are emailed. The emails are plain notices with one ordinary link to the issue: no tracking pixels and no tracked links. A notification that cannot be delivered is retried for up to 24 hours, then dropped. There is no weekly digest yet.

## Issue webhooks

Under the project's settings, **Issue webhooks** sends issue events to notification channels you already created. Pick the channels and the events: new issue (`created`), `resolved`, `regressed`, `assigned`, `escalated` and `github_linked`. Owners, admins and editors can change the setting; viewers can read it.

- **Webhook channels** receive a JSON body, signed exactly like alert webhooks (`X-Supermetry-Timestamp` and `X-Supermetry-Signature`, HMAC-SHA256 of `timestamp.body`):

  ```json
  {"type": "resolved",
   "issue": {"id": 12, "title": "...", "culprit": "...", "level": "error", "status": "resolved",
             "url": "https://.../app/<org>/<project>/issues/12", "first_seen": "...", "last_seen": "...", "events": 42},
   "actor": {"kind": "user", "name": "Ada"}}
  ```

  `actor.kind` is `user` or `system`. A member's name is included only when it is a plain display name; an address-like name is left out. The body never contains end-user identifiers, hashes or email addresses.
- **Slack, Discord, email and Telegram channels** receive one line of text.
- **Delivery** happens after the change is saved, from an outbox: a failing receiver is retried with growing delays (30 seconds, then 1, 2, 4 and 8 minutes) and dropped after six attempts. A deleted or disabled channel is skipped.

Through the private API: `GET` and `PUT /api/private/sites/{site}/app/issue-webhooks` with `{"channel_ids": [...], "kinds": [...]}`. Channels must belong to the project's organisation.

## User feedback

Sentry SDKs can ask people what went wrong: `Sentry.captureFeedback(...)`, the feedback widget, and the older user report dialog. Supermetry accepts these (`feedback` and `user_report` envelope items) and stops telling SDKs to skip them. Reports count toward the project's per-minute spike limit.

- The message is scrubbed like event text (emails, tokens, card numbers and the like are replaced) and cut to 5,000 characters. The page URL is reduced to its path.
- The reporter's **email address is never stored**: only a keyed hash, so the list can show that a contact address was given. Their **name is not stored** either. Names are kept only after an opt-in that does not exist yet.
- A report that names the event it is about is linked to that event's issue and appears under **User feedback** on the issue. A report for an unknown event is kept without a link and shows in the project's feedback list (`GET /api/private/sites/{site}/app/feedback`). Per issue: `GET .../issues/{issue}/feedback`.
- Feedback text is user content: it is part of the project export (`app_feedback.csv`, without any email) and is removed with the project.

## GitHub link

An issue can carry one link to an existing GitHub issue, `https://github.com/<owner>/<repo>/issues/<number>`. Other addresses are refused. Supermetry stores the link and never calls GitHub. Linking (not unlinking) can also be sent as a `github_linked` issue webhook.
### Merging and unmerging issues

Two issues that are really the same bug can be merged. Open the issue you want to keep, type the ids of the others in "Merged issues" and choose Merge. Owners, admins and editors can merge; viewers can read the list.

- The merged issues' events, fingerprints and daily and hourly counts move to the kept issue, whose counts are recomputed from its events. The merged issues disappear from the issue list, and their detail page says which issue they were merged into.
- New events that arrive with a merged issue's fingerprint land on the kept issue; they never create the old issue again. Merging a kept issue into a third one moves everything to the third, so a merge is always one level deep.
- All issues must belong to the same project. A merged issue cannot be merged again until it is unmerged.
- **Unmerge** splits one merged issue back out by its fingerprint hash, shown beside it in the list. Its events, including those that arrived after the merge, return to it, it reappears in the list, and both issues' counts and rollups are recomputed from events. Counts older than your retention period cannot be attributed to a fingerprint and stay with the kept issue.
- Both issues get a "merged" or "unmerged" row on their activity timeline.

Through the private API: `POST .../issues/{issue}/merge` with `{"source_ids": [..]}`, `GET .../issues/{issue}/merged`, and `POST .../issues/{issue}/unmerge` with `{"hashes": ["<64 hex characters>"]}`.

## Ownership and grouping rules

Project settings have two ordered rule lists. Owners, admins and editors can change them; viewers can read them. Each list holds up to 200 rules, and a value is a glob of at most 200 characters: `*` matches any run of characters (including `/`), `?` matches one character, and a backslash makes the next character literal. Matching is case-sensitive. The first matching rule wins, so order matters.

**Ownership rules** route a new issue to a member. A rule matches the file path or module of an in-app frame, or a tag (`path`, `module`, `tag:<key>`), and names the owner. The rule runs once, when an issue is first created: the issue is assigned and the timeline shows an `assigned` row by "System". It never changes the assignee of an existing issue, so unassigning stays unassigned, and an owner who has since left the project is skipped. Matching sees the original file names of symbolicated frames.

**Grouping rules** decide which events share an issue. A rule matches the error type, the message, a frame path or module, or a tag (`error_type`, `message`, `path`, `module`, `tag:<key>`) and gives a fingerprint of 1 to 64 characters. Matching events are grouped by that fingerprint instead of the computed one (an SDK fingerprint included), so different stacks can land in one issue. Put `{{ default }}` in the fingerprint to keep the default grouping and add to it, for example `{{ default }}-acme` splits the default groups by customer. Rules apply to new events only: issues that already exist are not regrouped.

Through the private API, `GET` and `PUT /api/private/sites/{site}/app/rules` read and replace both lists as `{"ownership": [{"matcher", "value", "owner_user_id"}], "grouping": [{"matcher", "value", "fingerprint"}]}`. A bad rule is a 400 that names its position, such as `ownership[1]`. Changes reach ingestion with the next site refresh, which the save triggers; no database lookup happens per event.

## Searching issues, saved views and bulk actions

The query bar above the issue list takes a search. Terms are separated by spaces and all must match.

| Term | Matches |
|---|---|
| `timeout`, `"null pointer"` | A bare word or quoted phrase in the title or culprit (case-insensitive substring) |
| `is:unresolved` | `unresolved` (open or regressed), `resolved`, `ignored`, `archived`, `snoozed`, `assigned`, `unassigned` |
| `assigned:me`, `assigned:none`, `assigned:ann@example.com` | Assignee. Owners, admins and editors can search by a member's email; for viewers an email never matches, so emails are not revealed |
| `release:1.4.0`, `environment:prod`, `platform:linux`, `process:renderer`, `level:fatal` | Issues that had at least one event with that value (`"quoted value"` if it has spaces) |
| `kind:crash`, `priority:high` | Issue kind and priority |
| `first-seen:-24h`, `last-seen:+30d`, `first-seen:2026-11-01` | `-` is within the last, `+` is older than that (units `m`, `h`, `d`, `w`); a date is that UTC day |
| `events:>10`, `users:<=3`, `events:7` | Events or affected users in the selected date range (`>`, `<`, `>=`, `<=`, or equal) |
| `!is:ignored`, `!release:1.0.0`, `!flaky` | A leading `!` negates any term |

An unknown key is an error that names the key and where it is; it is never ignored. A search can have at most 20 terms and 200 characters. Special characters are plain text: `%`, quotes and unicode only ever match literally. The search combines with the status tabs, kind, text and date filters of the page.

**Saved views** keep a search under a name (up to 80 characters). A private view is visible only to you; a shared view is visible to everyone who can open the project. You can delete your own views, and owners and admins can change or delete shared ones. Viewers can use shared views but cannot save any.

**Bulk actions:** tick issues in the list (or "Select all N matching issues") and resolve, ignore, archive, assign or set a priority in one step. Each change appears on every issue's activity timeline. At most 1,000 issues change at once, and a bulk change only ever touches issues of the project you are in. The private API is `POST /api/private/sites/{site}/app/issues/bulk` with `{"ids": [...], "change": {...}}` or `{"query": "...", "change": {...}}`; the reply lists `updated` ids and `skipped` ids that are not issues of this project. Saved views live under `.../app/views`. The search itself is the `query` parameter of `GET .../app/issues`; a syntax error is a 400 whose `position` is the character offset of the problem.

The issue list filters by the tags each issue's events carried (release, environment, platform, level, OS version, browser and device class). Those per-issue tag counts are rebuilt by the same rollup as the daily counts, which runs every ten minutes, so a very new event can take up to about ten minutes to be found by a `release:` or `level:` term.

## Releases and deploys

The release commands of `sentry-cli` and the release steps of the Sentry bundler plugins work against Supermetry unchanged. They need an upload token with the `releases:write` scope (see [Upload source maps](source-maps.md)) in `SENTRY_AUTH_TOKEN`, the server in `SENTRY_URL`, and the project in `SENTRY_PROJECT`:

```sh
sentry-cli releases new 1.4.0
sentry-cli releases set-commits 1.4.0 --auto
sentry-cli releases finalize 1.4.0
sentry-cli deploys new -r 1.4.0 -e production
```

`releases list`, `releases info`, `releases archive` and `releases delete` work too. Deleting a release removes its commits and deploys only; events keep their release name, so the release still appears in the dashboard.

**Commits come from your local git checkout.** `set-commits --auto` and `--local` read the commits (and the files each one changed) from the repository you run them in and send them, which is all Supermetry needs. Running the command again replaces the release's commit list. Updates that only name a repository and commit (`--commit "repo@sha"` without a checkout) are stored as the release's head commit, but Supermetry does not fetch commits from GitHub or other hosts yet.

In the dashboard:

- **Releases** lists every release with adoption and crash-free rates. Click a release for its page: adoption and crash-free sessions per day, its deploys, the issues first seen in it and the issues that regressed in it, and its commit count. Tick two releases and choose **Compare selected** to see them side by side with the change in crash-free sessions and users (in percentage points) and the issues that appear only in the second.
- **Deploy markers** appear as dashed lines on the overview chart and on a release's rollout chart, placed on the day of the deploy in the project's time zone. A deploy that names no project applies to the projects of its release.
- An issue's page shows the release it was **introduced in** and, when that release has commits, up to three **suspect commits** that changed a file on the stack.

An AI assistant can ask for the same data through the MCP tools `get_release_health`, `compare_releases` and `find_regression_release`; commit authors appear by name only.

## Exporting your data

An app project's ZIP export (`GET /api/private/sites/{site}/export?from=YYYY-MM-DD&to=YYYY-MM-DD`, needing the `data:export` scope) contains the usual web aggregate files and, for app projects only, these CSVs. Dates are in the project's time zone and timestamps in UTC.

| File | Contents |
|---|---|
| `app_issues.csv` | Issues active in the range: id, title, culprit, level, status, kind, error type, first and last seen, occurrences, first and last release, resolved-in release, priority, assignee id, GitHub link |
| `app_events.csv` | Stored events in the range: event id, timestamp, issue id, kind, level, release, dist, environment, platform, SDK name and version, OS version, architecture, device class, runtime, symbolication status, occurrences |
| `app_releases.csv`, `app_deploys.csv` | Releases and deploys created in the range that apply to the project |
| `app_artifacts.csv` | Source map bundle metadata only: bundle id, release, dist, state, debug IDs, file count, size, created. File contents and storage keys are never exported |
| `app_saved_views.csv` | Shared saved searches: id, name, query, owner id, created. Private views are not exported |
| `app_issue_activity.csv`, `app_issue_comments.csv` | The issue timeline and the comments written in the range (deleted comments are left out). Authors and actors appear as organisation member ids |
| `app_feedback.csv` | User feedback written in the range: id, issue id, event id, time, whether a contact address was given, text, page path. Reporter emails are stored only as a hash and are not exported |
| `app_client_reports.csv` | Daily counts of events the SDKs report they discarded, by reason and category. No event data |

The export never includes end-user identifiers or hashes, install or session hashes, IP data or the full event bodies. Rows stream out as they are read, so large ranges do not load into memory.

## Product analytics for apps

An app project also answers product questions: who uses the app, which screens and
features they use, and whether they come back. Open **Sessions & Users** and
**Events** in the project's sidebar.

### Where the data comes from

- **Sentry SDK sessions.** A Sentry SDK that has `release` set and sends a device id
  (the mobile and desktop SDKs do; browser SDKs do not) counts one `$app_open` event
  per session start. Session updates and aggregate session buckets add nothing, and a
  browser session without a device id is counted on **Data health** as
  `session_no_install` and creates no product session. Crash-free numbers are not
  affected either way.
- **`track()` and `screen()` in the Supermetry kits.** `track("purchase", { plan: "pro" })`
  records a custom event and `screen("paywall")` a screen view. Both need the kit's
  `installId`, are batched and queued like every other report (20 per request, offline
  queue, 7 days), check the name before sending (a name starting with `$` or with
  characters outside `A-Z a-z 0-9 _ . : $ - ` and space is ignored) and never send a user id.
  The same events can be sent to the [app events endpoint](#the-app-events-endpoint)
  with `kind` `screen` or `event`.

### What is counted

The install id is hashed with a per-app secret on arrival (the same HMAC that powers
crash-free users) and only the hash is stored, so an install is one stable pseudonymous
visitor across days: there is no daily salt, and an install that reports twice a month
is the same person both times. Raw install ids, device ids and user ids are never
stored. A screen is stored as a page (`/home`), the platform as the hostname, and the
release, device class and locale as reserved `$` properties.

Product hits are accepted up to `max report age` days late (7 by default). A hit for a
day that has already been rolled up marks the day for recalculation, so the charts
and rollups catch up; a hit older than the limit is dropped as `too_old`.

| Metric | Definition |
|---|---|
| Daily, weekly, monthly active installs | Distinct installs with a hit in the site's local day, in the 7 and 30 local days ending that day. Counts are never summed across days, so a range total is the distinct installs of the range |
| Stickiness | Mean daily active installs over the range divided by the distinct installs of the range |
| New installs | Installs whose first active day inside the retention window is that day |
| Release, platform, country, OS, device adoption | Installs per value and their share of the active installs. An install that used two values in the period counts once in each, so shares can add up to more than 100% |
| Engagement | Sessions, sessions per install, mean and median session length, screens per session |
| Funnels | Ordered screens and events inside one session. Count by installs counts the installs with any session that reached the step; count by sessions counts sessions |
| Retention | Day, week or month cohorts of installs, linked by the stable install id |
| Journeys | Screen to screen paths, counted in installs |
| Feature adoption | Installs that sent any event of a named set (up to 20 event names), as a share of the active installs |

Days follow the site's time zone, so a project in Asia/Kolkata or Asia/Kathmandu
has days that start at local midnight.

Funnels, retention, journeys and the events explorer need the instance's identified
tracking mode (`SUPERMETRY_TRACKING_MODE`), exactly like web sites: in anonymous mode they return the same
"history is not available" problem, and screen names are not stored. Active installs,
adoption and engagement keep working, because they use the hashed install id only.

### API and AI assistants

All reads are under `/api/public/v1/sites/{site_id}/app/product/` and need
`stats:read`: `active-users`, `adoption?by=release|platform|country|os|device`,
`engagement`, `features` and `features/{id}/adoption`. They take `range`, `release`,
`environment` and `platform`, and answer `404` for web sites. Funnels, retention and
journeys use their existing endpoints, which now also work for app projects.

The MCP server adds `get_product_overview`, `get_active_users`, `get_retention`,
`get_funnel`, `get_event_breakdown` and `get_user_journey`. They return counts only,
never install ids, hashes or profile identifiers.

## Ask your AI assistant

An AI assistant connected to the [MCP server](../api.md#mcp-for-ai-assistants) can read
an app's health with an API key that has the `stats:read` scope: the overview, open
issues, one issue with its latest events, the compatibility matrix and the releases.
The tools are read-only, so an assistant cannot resolve or ignore issues. `list_app_issues` takes an optional `query` in the search syntax above (without `assigned:me` or emails, which need a signed-in user), and `search_app_events` returns the newest events of the issues a query matches: event ids, times, issue ids, release and platform, up to 50, and never event bodies or user identifiers.
Two more tools read one issue's history: `get_issue_timeline` (the activity timeline, newest
first, at most 100 rows) and `get_issue_impact` (users, sessions and events over the last 30
days, with the releases and platforms they came from). They take `site` and `issue`, return
no emails or hashes, and show actors by display name only.
The tools are read-only, so an assistant cannot resolve or ignore issues.
