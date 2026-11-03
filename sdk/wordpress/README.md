# Supermetry for WordPress

A WordPress plugin (WordPress 6.0+, PHP 7.4+) that adds the Supermetry tracker, an optional
first-party proxy against ad blockers, and WooCommerce purchase tracking. The plugin lives in
[`supermetry/`](supermetry); its user-facing documentation is
[`supermetry/readme.txt`](supermetry/readme.txt) (WordPress.org format).

## Install

Zip the `supermetry` directory and upload it under Plugins → Add New → Upload Plugin, or copy
it to `wp-content/plugins/`:

```sh
cd sdk/wordpress && zip -r supermetry.zip supermetry
```

Then open **Settings → Supermetry** and enter the Supermetry URL
(`https://app.supermetry.com` for Cloud) and the site ID.

## What it does

| Feature | How |
|---|---|
| Tracker | `wp_enqueue_script` in `<head>` with `defer`: `<script defer data-site="ID" src="HOST/js/ID.js">`, the same per-site script the dashboard snippet uses |
| Exclusions | Logged-in users with the selected roles (default: administrators) get no tracker and send no purchases |
| Automatic events | Outbound links, file downloads and forms are switched on per site in Supermetry; unchecking one adds `data-outbound="false"` (etc.) to turn it off here. "Hash routing" adds `data-hash="true"` |
| First-party proxy | REST routes under `/wp-json/sm/v1/` mirror the paths the [ad blockers guide](../../docs/guide/ad-blockers.md) proxies: `js/<site>.js` (cached in a transient for 5 minutes), `api/public/v1/events`, `replay.js` and `api/replay/*`. The upstream is always the configured host; only the configured site's script and events are relayed |
| Visitor identity | Proxied and server-side requests forward `User-Agent`, `X-Forwarded-For`, `DNT`, `Sec-GPC` and `Accept-Language`, never cookies. With a proxy key (Cloud) they also send `X-Supermetry-Proxy-Key` and `X-Supermetry-Client-IP` |
| WooCommerce | On `woocommerce_thankyou`, one server-side `Purchase` event with `revenue: {amount, currency}`, deduplicated by order meta and a stable event ID |

Filters: `supermetry_should_track`, `supermetry_script_attributes`, `supermetry_script_dir`,
`supermetry_script_cache_ttl`, `supermetry_woocommerce_purchase_props`.

## License

The plugin is licensed **GPL-2.0-or-later**, as WordPress.org requires for hosted plugins and
to match WordPress itself. The rest of this repository is Apache-2.0.

## Development

```sh
cd sdk/wordpress
composer install          # WordPress Coding Standards + PHPCompatibilityWP
vendor/bin/phpcs          # lint
find supermetry -name '*.php' -exec php -l {} \;
```

Regenerate the translation template with WP-CLI:

```sh
wp i18n make-pot supermetry supermetry/languages/supermetry.pot --domain=supermetry
```
