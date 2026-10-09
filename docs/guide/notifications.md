# Notification channels

Organization owners and admins can create notification channels under **Organization settings → Notifications**. Editors can use configured channels when creating site alerts, but cannot change credentials. Viewers cannot read channels.

Creating a channel does not subscribe it to existing alerts. Add it explicitly to each alert that should use it. Disabling a channel pauses automatic delivery without removing it from alert rules. The **Send test** action is manual and remains available while a channel is disabled so its configuration can be checked before enabling it.

## Telegram

Supermetry uses Telegram's HTTPS Bot API `sendMessage` method at the fixed `api.telegram.org` endpoint. It sends plain text only; alert text is limited to Telegram's 4,096-character maximum. Supermetry does not accept a custom Telegram API URL.

1. Create or choose a bot with [BotFather](https://core.telegram.org/bots/features#botfather) and copy its token.
2. Add the bot to the target chat or channel and grant it permission to post there.
3. Find the destination's numeric chat ID, including the leading minus sign for groups and channels, or use a public channel username such as `@example_alerts`.
4. In Supermetry, add a **Telegram** channel, enter a descriptive name, bot token, and chat ID or channel username.
5. Leave the channel disabled, save it, and use **Send test**. Enable it only after the test succeeds, then select it on the intended site alerts.

The bot token is encrypted with `SUPERMETRY_SECRET_KEY` using the same authenticated secret storage as other notification credentials. It is write-only: the API and UI report only that a token is configured. Leaving the token field blank while editing keeps the current token; entering a new token replaces it. Tokens are not included in logs, audit metadata, errors, or exports. Back up `SUPERMETRY_SECRET_KEY`; changing or losing it makes saved notification credentials unreadable.

Telegram puts the bot token in the request URL path. Supermetry therefore uses a dedicated client, never returns underlying HTTP errors, never follows redirects, and never includes the URL or Telegram response description in errors or logs. Requests time out after five seconds and honor cancellation.

Supermetry retries only when Telegram explicitly rejects a request with HTTP 429 and supplies `parameters.retry_after`. The wait and attempt count are bounded. Network errors, timeouts, redirects, malformed responses, and server errors are not retried because the message may already have been accepted.

See the official [Bot API request format and response model](https://core.telegram.org/bots/api#making-requests), [`sendMessage` parameters](https://core.telegram.org/bots/api#sendmessage), and [`ResponseParameters.retry_after`](https://core.telegram.org/bots/api#responseparameters).

## Delivery failures

A failed test returns a safe message asking the manager to verify the credentials and destination. Automatic delivery attempts every enabled channel independently; one failing destination does not prevent the remaining destinations from being attempted. Failures increment the existing alert-delivery health counter and log only the alert ID, channel ID, channel kind, and a sanitized error.
