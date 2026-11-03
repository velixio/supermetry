# Supermetry for Shopify

Cookie-free analytics for Shopify stores. Two parts:

| Part | Path | What it does |
| --- | --- | --- |
| Theme app embed | `extensions/supermetry-theme` | Adds the Supermetry tracker to every storefront page (page views, outbound links, file downloads, custom events). |
| Web pixel | `extensions/supermetry-pixel` | Runs in Shopify's pixel sandbox (including checkout, where themes can't add scripts) and sends `Product Viewed`, `Add to Cart`, `Checkout Started` and `Purchase` (with revenue) events. |

You can install them as a Shopify app (below) or, without an app, paste a
theme snippet and a custom pixel ([No-app setup](#no-app-setup)).

## Events

| Shopify event | Supermetry event | Properties |
| --- | --- | --- |
| `page_viewed` | page view | see `pageviews` setting |
| `product_viewed` | `Product Viewed` | `product`, `product_id`, `variant`, `variant_id`, `sku`, `price` |
| `product_added_to_cart` | `Add to Cart` | same as above + `quantity`, `value` |
| `checkout_started` | `Checkout Started` | `items`, `value` |
| `checkout_completed` | `Purchase` | `order_id`, `items`, `coupon`; revenue = `checkout.totalPrice` |

`Purchase` uses the event id `shopify_order_<order id>`, so Supermetry counts
an order once even if the thank-you page is reloaded. Revenue is only stored
when the order currency matches the site currency in Supermetry (the event is
still counted otherwise).

**Page views.** The pixel's `pageviews` setting avoids double counting:
`checkout` (default) counts only checkout pages because the theme embed counts
the storefront; `all` counts every page (use when the embed is off); `none`
disables pixel page views.

## Before you start

In Supermetry, create a site for the store and copy its 12-character site ID
(Settings → Installation). If the site restricts allowed hostnames, add the
storefront domain, the `*.myshopify.com` domain and the checkout domain. Pixel
events come from Shopify's sandbox origin, so keep strict origin checks off for
this site or allow those origins.

## App setup

Requires a [Shopify Partner](https://partners.shopify.com) account, Node 18+ and
the [Shopify CLI](https://shopify.dev/docs/api/shopify-cli).

1. Create an app in the Partner dashboard (Apps → Create app → Create app
   manually), or let the CLI create one in step 2.
2. Link this folder to the app and deploy both extensions:

   ```sh
   cd sdk/shopify
   npm install
   npx shopify app config link   # fills client_id in shopify.app.toml
   npx shopify app deploy
   ```

3. Install the app on your store (Partner dashboard → app → Test on
   development store, or a custom distribution link for a merchant store).
4. **Theme embed:** Online Store → Themes → Customize → App embeds → turn on
   *Supermetry*, enter the site ID (and your Supermetry URL when
   self-hosting), save.
5. **Pixel:** web pixels are connected through the Admin API with the app's
   access token. The quickest way is `npx shopify app dev`, open GraphiQL
   (press `g`) and run:

   ```graphql
   mutation ($settings: JSON!) {
     webPixelCreate(webPixel: { settings: $settings }) {
       userErrors { code field message }
       webPixel { id settings }
     }
   }
   ```

   with variables:

   ```json
   { "settings": "{\"host\":\"https://app.supermetry.com\",\"site\":\"YOUR_SITE_ID\",\"pageviews\":\"checkout\"}" }
   ```

   Use `webPixelUpdate` to change settings later. A public app would run this
   mutation from its own backend after install; this template has no backend.

Check Settings → Customer events in the Shopify admin: the Supermetry app
pixel should be listed as *Connected*.

### Development

```sh
npm test                          # payload mapping tests (node:test)
node scripts/build-custom-pixel.mjs   # regenerate no-app/custom-pixel.js
```

`extensions/supermetry-pixel/src/mapping.js` is the single source of the event
mapping; `no-app/custom-pixel.js` is generated from it and a test fails if it
is out of date.

## No-app setup

1. **Storefront:** Online Store → Themes → ⋯ → Edit code → `layout/theme.liquid`.
   Paste [`no-app/theme-snippet.liquid`](no-app/theme-snippet.liquid) just
   before `</head>` and replace `YOUR_SITE_ID` (and the host when
   self-hosting).
2. **Checkout and commerce events:** Settings → Customer events → Add custom
   pixel → name it *Supermetry* → paste
   [`no-app/custom-pixel.js`](no-app/custom-pixel.js), set `site` (and `host`)
   at the top, Save → Connect. Under *Customer privacy*, choose
   *Not required* only if your legal assessment allows cookie-free analytics
   without consent; otherwise keep *Analytics*.

Keep `pageviews: "checkout"` when the theme snippet is installed, or set
`"all"` when the custom pixel is your only tracker.

## Notes

- Pixel requests use `fetch` with `keepalive` and a text body (no CORS
  preflight), like the browser tracker; no cookies or identifiers are stored.
- The theme embed doesn't render in the theme editor preview, so editing the
  theme doesn't inflate stats.
- Custom-event tracking from the theme (`supermetry.track(...)`) works as
  described in the [JS API docs](../js/README.md) once the embed is on.
