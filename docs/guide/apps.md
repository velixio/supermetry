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

**Set `release`: Sentry SDKs send no sessions without it.** Without a release
you get issues but no crash-free sessions or users.

The DSN public key is meant to ship inside client bundles, so it is shown in
full. Under **DSN keys** you can create more keys (for example one per
environment), give each a label, restrict a key to allowed origins (browser
requests from other origins are refused), and revoke a key at once. A key
belongs to one app project.

An issue lists its latest events. **Full event detail** shows the exception
chain with in-app frames highlighted, breadcrumbs, contexts (OS, device, app,
runtime, browser), tags and attachment names. Event detail is kept for 30 days;
the issue and its counts are kept longer. Messages, emails, tokens, IP addresses,
cookies and query strings are scrubbed before storage. See
[Migrate from Sentry](migrate-from-sentry.md) for what is and is not supported.

The sections below describe Supermetry's own app endpoint and client kits, still
supported and listed under **Legacy kits** on the Installation page.

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
| `kind` | enum | Required: `crash`, `error`, `hang`, `install_failed`, `install_stalled`, `incompatibility`, `operation_failed`, `session`. |
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
- **Archive until escalating:** removes the issue from the open list and keeps it out of the way. Unarchive it to return it to open.
- **GitHub link:** the API can link an existing GitHub issue (`https://github.com/<owner>/<repo>/issues/<number>`). Supermetry only stores the link and never calls GitHub.
- **Comments:** up to 10,000 characters each. You can edit or delete your own comments; owners and admins can edit or delete anyone's. Comments are visible to everyone in the project, so keep secrets and personal data out of them.
- **Activity:** a timeline of every change (status, assignee, priority, snooze, archive, comments) plus system rows for the first sighting and for regressions. It records the member who acted, or "System".

Through the private API, `PATCH /api/private/sites/{site}/app/issues/{issue}` takes any of `status`, `resolved_in_release`, `assignee` (a user id, `0` to unassign), `priority`, `snooze` (`until`, `events`, `users`), `archive` and `github_url`. Comments live under `.../issues/{issue}/comments` and the timeline at `.../issues/{issue}/activity`. A change that alters nothing writes no activity row.

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

## Exporting your data

An app project's ZIP export (`GET /api/private/sites/{site}/export?from=YYYY-MM-DD&to=YYYY-MM-DD`, needing the `data:export` scope) contains the usual web aggregate files and, for app projects only, these CSVs. Dates are in the project's time zone and timestamps in UTC.

| File | Contents |
|---|---|
| `app_issues.csv` | Issues active in the range: id, title, culprit, level, status, kind, error type, first and last seen, occurrences, first and last release, resolved-in release, priority, assignee id, GitHub link |
| `app_events.csv` | Stored events in the range: event id, timestamp, issue id, kind, level, release, dist, environment, platform, SDK name and version, OS version, architecture, device class, runtime, symbolication status, occurrences |
| `app_releases.csv`, `app_deploys.csv` | Releases and deploys created in the range that apply to the project |
| `app_artifacts.csv` | Source map bundle metadata only: bundle id, release, dist, state, debug IDs, file count, size, created. File contents and storage keys are never exported |
| `app_issue_activity.csv`, `app_issue_comments.csv` | The issue timeline and the comments written in the range (deleted comments are left out). Authors and actors appear as organisation member ids |
| `app_client_reports.csv` | Daily counts of events the SDKs report they discarded, by reason and category. No event data |

The export never includes end-user identifiers or hashes, install or session hashes, IP data or the full event bodies. Rows stream out as they are read, so large ranges do not load into memory.

## Ask your AI assistant

An AI assistant connected to the [MCP server](../api.md#mcp-for-ai-assistants) can read
an app's health with an API key that has the `stats:read` scope: the overview, open
issues, one issue with its latest events, the compatibility matrix and the releases.
The tools are read-only, so an assistant cannot resolve or ignore issues.
