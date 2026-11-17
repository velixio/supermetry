# Supermetry for Ghost

Ghost has no server-side plugins, so Supermetry integrates in two optional
layers:

| Option | Path | Use when |
| --- | --- | --- |
| Code injection | [`code-injection.html`](code-injection.html) | Any Ghost site, including Ghost(Pro). No theme changes. |
| Theme partial | [`partials/supermetry.hbs`](partials/supermetry.hbs) | You maintain a custom theme and want the site ID in Design settings. |

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
  fire again. Client-side events carry no revenue. Server-side member events (signups,
  paid subscriptions) are planned as a direct Ghost webhook endpoint in Supermetry.

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

### Tests

```sh
cd sdk/ghost && npm test   # node:test: snippets
```
