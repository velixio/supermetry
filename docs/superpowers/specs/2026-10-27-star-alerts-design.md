# GitHub star alerts: design

Date: 2026-10-27. Status: accepted.

## Intent

Tell an organization when something notable happens to a tracked repository, through the notification channels the organization already has (email, Slack, Discord, Telegram, webhook; Settings, Notifications). No new channel system.

## Settings (per tracked repository)

- Events, all off by default: `star_milestone`, `star_spike`, `unstar_surge`, `trending_entered`, `trending_left` (`github_star_alert_settings.events`).
- Channels: ids of the organization's existing channels (`github_star_alert_channels`, foreign key to `notification_channels` with ON DELETE CASCADE, so deleting a channel removes it from every repository). The service rejects any id that is not in the repository's own organization.
- Owners and admins change them; every member can read. Routes: `GET`/`PUT /api/private/orgs/{org}/github-stars/repos/{id}/alerts`, `POST .../alerts/test` (owner/admin, one per 30 s per repository). Changes are audited as `github_stars.alerts_updated`.

## Evaluation

`ghstars.Service.AlertsJob()` runs every 10 minutes (`EvaluateAlerts`) for enabled repositories of live organizations that have a settings row.

- **Milestone** at 100, 250, 500, 1,000, 2,500, 5,000, 10,000, 25,000, 50,000, 100,000 on the GitHub-verified count. The last count seen is kept in `github_star_alert_state`; the first evaluation only records it, so enabling alerts on a repository with 5,000 stars sends nothing. A rise over several milestones sends only the highest. Key `repoid:milestone`.
- **Spike**: stars gained in the last 6 hours >= max(10, 3 x the average per 6 hours over the 14 days before that window, i.e. total / 56). Key `repoid:spike:<utc date>:<hour/6>`.
- **Unstar surge**: same with unstars, floor 5. Key `repoid:unstar:<utc date>:<hour/6>`.
- Window counts leave out events from the history import (`source = backfill`); rate alerts wait until the import finished. The baseline counts every source, so a repository younger than 14 days is compared with a lower average and the floors apply.
- **Trending**: the trending watcher calls `ghstars.StarAlerts.TrendingEntered/TrendingLeft(ctx, repoID, TrendingDetails{Period, Language, Rank})`. The trending watcher's `ghtrending.TrendingNotifier` is connected by `cmd/supermetry/trending_alerts.go`; the key is `repoid:kind:episode<id>`, so each episode alerts once when it opens and once when it closes (without an episode id it falls back to date, period and language). The 10 minute per-kind interval applies, so several lists entered in the same run send one alert.

## Delivery

- `alerts.Dispatcher.DeliverMessage` sends a finished message through a channel's existing config and senders (no alert template).
- Dedup: `github_star_alert_log(repo_id, kind, key)` is unique, so an event goes out once. Throttle: at most one alert per repository and kind every 10 minutes; later ones in the window are dropped.
- Nothing is recorded when no channel would receive the alert, so ticking an event later is never blocked by used-up keys. If every channel fails, the claim is released.
- Best effort: one failing channel does not stop the others, errors are scrubbed of URLs and tokens, counters are exposed by `Service.AlertStats()`, and the star pipeline never waits on or fails because of an alert.
- Messages carry the repository name, counts and a github.com link only, never stargazer logins.
