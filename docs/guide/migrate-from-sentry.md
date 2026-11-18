# Migrate from Sentry or GlitchTip

Supermetry accepts events from unmodified Sentry SDKs. You do not change your
capture code: you change the DSN, set a release, and keep the SDK's default
settings. This guide covers Sentry (cloud or self-hosted) and GlitchTip, which
use the same SDKs and protocol.

## 1. Create the project and copy the DSN

Create an app project (Sites, New, App). **Settings, Installation** shows the DSN
and a snippet for your SDK. The DSN has the same shape as a Sentry DSN:
`https://<public key>@<your host>/<project id>`.

## 2. Change the DSN

Replace the `dsn` option in `Sentry.init` (or the equivalent in your SDK) with the
new DSN. Nothing else about your capture code changes. Setup snippets exist for
JavaScript (browser), Node, Electron, Python, Go, Android and Cocoa. Other
Sentry SDKs that use the standard envelope endpoint work the same way.

## 3. Set `release`

Set `release` in the same `init` call, for example `my-app@1.0.0`. **Sentry SDKs
send no sessions without a release**, so without it you get issues but no
crash-free sessions or users, and no regression detection by release.

## 4. Browsers: allowed origins

The DSN public key is public by design and ships in your bundle. For browser
apps, create a DSN key with **allowed origins** (`https://app.example.com`); a
browser request from another origin is refused. Revoke a key from the same
screen if it is abused. You can keep one key per environment.

## What is supported

- Errors and messages with exception chains, stack frames (in-app frames are used
  for grouping), breadcrumbs, contexts, tags, release, environment and SDK info.
- Sessions: release health, crash-free users and sessions.
- Client reports (counted as dropped events) and attachment metadata (name, type
  and size; contents are not stored).
- CSP violation reports sent to the `security` endpoint.

## Source maps

Keep using `sentry-cli`, `@sentry/vite-plugin` or another Sentry bundler plugin.
Create an upload token under Settings, Installation, set `SENTRY_AUTH_TOKEN`
(plugins: `authToken` and `url`) and upload as before. See
[Upload source maps](source-maps.md).

## Releases and deploys

`sentry-cli releases new`, `set-commits --auto`, `finalize`, `list`, `archive`, `delete`
and `deploys new` work unchanged with the same upload token. Commits are read from your
local git checkout; Supermetry does not fetch them from GitHub. See
[Releases and deploys](apps.md#releases-and-deploys).

## Native and mobile symbols

The Android Gradle plugin, the Xcode build phase and `sentry-cli debug-files upload`
work against Supermetry with the same variables as for source maps. ProGuard/R8
mappings, dSYM, Mach-O and ELF files are accepted and used to symbolicate crashes.
See [Native and mobile symbols](apps.md#native-and-mobile-symbols) for what is
supported and for the files that are only stored.

## What is not supported yet

- **Performance**: transactions, spans and traces.
- **Session replay**, **profiles**, **cron monitors**, **user feedback**, logs and
  metrics.
- Native crash dumps (minidumps) are counted but not processed. Crashes sent as
  events by the native SDKs are symbolicated from uploaded debug files.

### Why the SDK stops sending performance data

Every successful reply carries an `X-Sentry-Rate-Limits` header that tells the SDK
to stop sending the categories Supermetry does not store yet (transactions,
spans, profiles, replays, logs, check-ins and feedback). SDKs honour it, so you
do not pay the bandwidth for data that would be thrown away. Errors, sessions and
client reports keep flowing. When a category becomes supported, the header drops
it and SDKs resume sending it.

## What is kept and what is dropped

Supermetry stores no IP addresses (the IP is used for the country, then
discarded) and no user email, name or username. A user ID is stored as a keyed
hash. Messages and exception values are scrubbed of emails, tokens, card numbers,
IP addresses and home paths. Request cookies, the `Authorization` header, query
strings and bodies are dropped, and frame variables are never stored. Event
detail is kept for 30 days.

## Old events

Events already in Sentry or GlitchTip are not imported. Keep the old project
read-only for as long as you need the history, and let new events flow to
Supermetry.
