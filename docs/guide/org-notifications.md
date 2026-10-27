# Organization notifications (Telegram)

Organization owners and admins can connect a Telegram chat to receive messages about organization-level events, such as a GitHub repository reaching a star milestone. This is separate from site alerts, which are configured per site under Alerts.

## Set it up

1. In Telegram, open @BotFather, send `/newbot`, and follow the prompts. Copy the bot token.
2. Add the bot to the chat or channel that should receive messages. For a channel, make the bot an administrator.
3. Find the chat id. Send any message in the chat, then open `https://api.telegram.org/bot<token>/getUpdates` in a browser and look for `chat.id`. Group ids start with a minus sign. A public channel can use its `@username` instead. Do not share that address: it contains your token.
4. In Settings, Notifications, fill in the Telegram card: token, chat id, and tick the events you want. Every event is off until you tick it. Save, then press Send test message.

## Events

| Event | When |
|---|---|
| Star milestones | A tracked repository reaches a round star count |
| Star spikes | Stars arrive much faster than usual |
| Unstar surges | An unusual number of stars is removed in a short time |
| Entered trending | A tracked repository appears on GitHub trending |
| Left trending | A tracked repository drops off GitHub trending |

## Behaviour

- Each event is sent once. The same event key is never repeated for an organization.
- At most one message per event kind every 10 minutes per organization, so a burst cannot flood the chat. Events that arrive inside that window are dropped, not queued.
- Delivery is best effort. After 10 failures in a row the channel turns itself off and the status line says why. Fix the problem and save again with Enabled on to resume.
- The bot token is encrypted with the instance secret key, is never shown again after saving, and never appears in logs, errors or audit entries. Leave the token blank when editing to keep the stored one.
- Messages contain repository names, counts and links only, never the names or emails of stargazers.
- The instance only contacts `https://api.telegram.org`. Self-hosted instances need outbound HTTPS to it.

## API

`GET /api/private/orgs/{org}/notifications`, `PUT` and `DELETE /api/private/orgs/{org}/notifications/telegram`, and `POST /api/private/orgs/{org}/notifications/telegram/test` (one test per 30 seconds per organization). Owners and admins only. See `internal/api/openapi.yaml`.
