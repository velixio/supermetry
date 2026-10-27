# Organization notification channels: design

Date: 2026-10-27. Status: accepted (design decided by the user; this note records it).

## Intent

A generic, self-hostable way for features (GitHub star alerts first, others later) to notify an organization's owners. Telegram is the first channel kind. No Cloud-specific code; core has its own small Telegram client in `internal/notifications`.

## Data

- `org_notification_channels(id, org_id FK cascade, kind in ('telegram'), enabled, config_enc, chat_id, events jsonb, created_at, updated_at, last_ok_at, last_error, consecutive_failures)`, unique `(org_id, kind)`. `config_enc` is the bot token sealed with the instance secret box (info string `supermetry/org-notifications/v1`, HKDF from the instance secret key, same facility as GitHub star tokens).
- `org_notification_log(org_id, kind, key, sent_at)`, primary key `(org_id, kind, key)`: dedup plus throttle state. Rows older than 30 days are pruned on the next send of that kind.
- Migration `0247_org_notification_channels.sql`.

## Event kinds

Registry in Go (`notifications.Kinds()`): `star_milestone`, `star_spike`, `unstar_surge`, `trending_entered`, `trending_left`. Every kind is off until the owner ticks it. Adding a kind is one registry entry plus a constant.

## Interface for other packages

```go
type Notifier interface {
    Notify(ctx context.Context, orgID int64, event Event)
}
type Event struct{ Kind Kind; Key, Title, Body string }
```

`Service` implements it; `Noop` and `Fake` (records events) are for callers and tests. Notify never returns an error and never waits for the network.

## Delivery rules

1. Find enabled channels of the org whose `events[kind]` is true. None: return, record nothing (so a later opt-in is not blocked by used-up keys).
2. In one transaction under an advisory lock per (org, kind): drop if any send of that kind happened within the minimum interval (default 10 minutes); otherwise insert `(org, kind, key)` and drop if it already existed (dedup).
3. Deliver in a background goroutine (bounded to 16 in flight; beyond that the claim is released and the event dropped with a log line). Timeout 30 s overall.
4. On success: `last_ok_at` set, failures reset. On failure: `consecutive_failures + 1`, `last_error` set to the scrubbed message. At 10 failures the channel is disabled and `last_error` says so. Saving with Enabled on (or a new token) clears the failure state.
5. If every channel failed, the log claim is released so the next event is neither throttled nor deduplicated against a message nobody received.

## Telegram client

Fixed host `https://api.telegram.org`, https only, no redirects (a 3xx is an error), no proxy, 10 s timeout, response capped at 16 KiB, plain text (no parse mode), text capped at 4000 characters, `sendMessage` only. Errors are generic strings; every stored or returned error also passes through `Scrub`, which removes the token and anything shaped like one.

## API

Under `/api/private/orgs/{org}/notifications`, owners and admins (`channels.manage`); editors and viewers get 403, other orgs 404:

- `GET` lists channels plus the event kind registry. The token is only reported as `token_set`.
- `PUT /telegram` creates or updates. `token` is required on create and optional on update.
- `DELETE /telegram`.
- `POST /telegram/test` sends a test message; 200 `{ok}` or `{ok:false,error}` (scrubbed); 429 when called again within 30 s for the same org (per process, in memory).

Every change is audited as `org_notification_channel.saved|deleted|tested` with kind, enabled flag, event names and `token_changed`; never the token or chat id.

## UI

A Telegram card on the existing org Notifications settings page (owners and admins only): token (password input, "Saved" placeholder), chat id, enabled switch, one checkbox per event with its description, Save, Send test message, Remove, a status line (last delivery, failures in a row, last error) and BotFather help text.

## Privacy

Messages carry repository names, counts and links only. Callers must never put stargazer logins, emails or other personal data in `Title` or `Body`. The bot token never leaves the server after saving, is not logged, and is scrubbed from all error strings.

## Known limits

- The test rate limit and the in-flight bound are per process; a multi-instance deployment gets them per instance (the dedup and throttle are in Postgres and hold across instances).
- Events dropped by the throttle are not queued.
- Only one Telegram channel per org (unique `(org_id, kind)`).

## First consumers: GitHub star alerts

`ghstars.Service.AlertsJob()` (every 10 minutes) calls `EvaluateAlerts`, which uses `Service.Notifier`. Per enabled repository of a live organization:

- **Milestone** (100, 250, 500, 1,000, 2,500, 5,000, 10,000, 25,000, 50,000, 100,000): uses the GitHub-verified star count and the last count seen (`github_star_alert_state`). The first evaluation only records a baseline, so enabling a repository with 5,000 stars sends nothing. When the count rises over one or more milestones, only the highest is sent. Key `repoid:milestone`.
- **Spike**: stars gained in the last 6 hours >= max(10, 3 x the average per 6 hours over the 14 days before that window, i.e. total / 56). Key `repoid:spike:<utc date>:<hour/6>`.
- **Unstar surge**: same with unstars and a floor of 5. Key `repoid:unstar:<utc date>:<hour/6>`.
- Window counts ignore events from the history import (`source = backfill`); rate alerts are skipped until the import is complete. The baseline counts every source. A repository younger than 14 days is compared with a lower average, so the floor applies.
- Messages carry the repository name, counts and a github.com link only.
