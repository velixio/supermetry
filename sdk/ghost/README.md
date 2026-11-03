# Supermetry for Ghost

Ghost has no server-side plugins, so Supermetry integrates in three optional
layers:

| Option | Path | Use when |
| --- | --- | --- |
| Code injection | [`code-injection.html`](code-injection.html) | Any Ghost site, including Ghost(Pro). No theme changes. |
| Theme partial | [`partials/supermetry.hbs`](partials/supermetry.hbs) | You maintain a custom theme and want the site ID in Design settings. |
| Webhook relay | [`webhook-relay/`](webhook-relay) | You want server-side member events (signups, paid subscriptions with revenue) that ad blockers can't block. |

First create a site in Supermetry for the blog's domain and copy its
12-character site ID (Settings → Installation).

## Code injection

Ghost Admin → Settings → Code injection → **Site header**, paste
[`code-injection.html`](code-injection.html) and replace `YOUR_SITE_ID` (twice)
and, when self-hosting, `https://app.supermetry.com`:

```html
<script defer data-site="YOUR_SITE_ID" src="https://app.supermetry.com/js/YOUR_SITE_ID.js"></script>
```

The second `<script>` block in the file is the member-events snippet below; drop
it if you only want page views. Any other tracker option from the
[script tag docs](../README.md) (`data-hash`, `data-consent`, `data-outbound="false"`…)
can be added to the first tag.

## Member events (client-side)

Ghost Portal redirects back to the site with query parameters after a member
action. The snippet ([`member-events.js`](member-events.js), already included
in `code-injection.html` and the theme partial) turns them into custom events:

| URL after redirect | Event |
| --- | --- |
| `?action=signup&success=true` (also `signup-paid`, `subscribe`) — member clicked the signup magic link | `Signup` |
| `?stripe=success` — returned from Stripe Checkout | `Subscription` |

Both carry `source: ghost_portal`. Create goals for `Signup` and
`Subscription` in Supermetry to see conversion rates. Notes:

- Sign-ins (`action=signin`) are ignored.
- Paid tiers with a custom *welcome page* redirect with `action=signup`
  instead of `stripe=success`, so those checkouts count as `Signup`.
- Portal removes the parameters from the address bar, so reloading doesn't
  fire again. Client-side events carry no revenue; use the webhook relay for
  revenue.

## Theme partial

1. Copy [`partials/supermetry.hbs`](partials/supermetry.hbs) into your theme's
   `partials/` folder.
2. Add the settings to the theme's `package.json` (`config.custom`, max 20
   custom settings per theme):

   ```json
   {
     "config": {
       "custom": {
         "supermetry_site_id": { "type": "text", "default": "" },
         "supermetry_host": { "type": "text", "default": "https://app.supermetry.com" },
         "supermetry_member_events": { "type": "boolean", "default": true }
       }
     }
   }
   ```

3. In `default.hbs`, add `{{> supermetry}}` just before `{{ghost_head}}`.
4. Upload the theme, then Settings → Design → *Site wide* → set **Supermetry
   site id** (and **Supermetry host** without a trailing slash when
   self-hosting).

Nothing renders until the site ID is set. Don't also use the code-injection
snippet, or page views count twice.

## Webhook relay (server-side member events)

A dependency-free Node.js (18.17+) service that receives Ghost webhooks,
verifies the `X-Ghost-Signature` HMAC and sends events to Supermetry's
`/api/public/v1/events` endpoint.

| Ghost webhook | Supermetry event | Details |
| --- | --- | --- |
| `member.added` | `Signup` | id `ghost_signup_<member id>`; `status` prop |
| `member.added` with status `paid`, or `member.edited` where status changes to `paid` | `Subscription` | id `ghost_sub_<subscription id>`; `tier`, `interval` props; revenue from the active Stripe price (trials have none) |

Event IDs are stable, so Ghost's retries and duplicate deliveries count once.
No email, name or other member data is sent; set `SUPERMETRY_PROFILE_ID=uuid`
to attach the member's Ghost UUID as `profile_id` (only if your privacy policy
allows it). Revenue is stored only when the price currency matches the site
currency in Supermetry.

Run it:

```sh
cd sdk/ghost/webhook-relay
GHOST_WEBHOOK_SECRET=... GHOST_URL=https://blog.example.com \
SUPERMETRY_SITE=YOUR_SITE_ID node index.js
# or: docker build -t supermetry-ghost-relay . && docker run -p 8787:8787 -e ... supermetry-ghost-relay
```

| Variable | Default | |
| --- | --- | --- |
| `GHOST_WEBHOOK_SECRET` | — | Required. Secret from the Ghost integration's webhook. |
| `GHOST_URL` | — | Required. Public site URL; used as the event URL and `Origin`. Must be an allowed host of the Supermetry site. |
| `SUPERMETRY_SITE` | — | Required. Site ID. |
| `SUPERMETRY_HOST` | `https://app.supermetry.com` | Self-hosted Supermetry URL. |
| `PORT` / `BIND` | `8787` / `0.0.0.0` | Listen address. |
| `WEBHOOK_PATH` | `/ghost` | Path Ghost posts to. `GET /healthz` returns `ok`. |
| `SUPERMETRY_SIGNUP_EVENT` / `SUPERMETRY_SUBSCRIPTION_EVENT` | `Signup` / `Subscription` | Event names. |
| `SUPERMETRY_PROFILE_ID` | `none` | `uuid` sends the member UUID as `profile_id`. |
| `SIGNATURE_TOLERANCE_SECONDS` | `300` | Maximum signature age; `0` disables the check. |

Then in Ghost Admin → Settings → Integrations → *Add custom integration* →
*Add webhook*: event **Member added**, target
`https://relay.example.com/ghost`, and set a **secret**. Add a second webhook
for **Member updated** with the same URL and secret. Serve the relay over
HTTPS (e.g. behind your reverse proxy).

The relay answers `202` after forwarding, `401` for bad signatures and `502`
when Supermetry is unreachable or returns 5xx/429, so Ghost retries.

**Use either client-side member events or the relay**, not both with the same
event names, or conversions count twice (rename the relay's events if you want
both). Server-side events come from the relay's IP and user agent, so they
count conversions and revenue but not as the visitor's own session. The relay
sends `Origin` from `GHOST_URL`, so *strict origin* works as long as that host is
allowed for the site.

### Tests

```sh
cd sdk/ghost && npm test   # node:test: snippets, signature and payload mapping
```
