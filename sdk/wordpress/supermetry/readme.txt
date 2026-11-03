=== Supermetry Analytics ===
Contributors: supermetry
Tags: analytics, statistics, privacy, woocommerce, cookie-free
Requires at least: 6.0
Tested up to: 6.7
Requires PHP: 7.4
Stable tag: 0.1.0
License: GPL-2.0-or-later
License URI: https://www.gnu.org/licenses/gpl-2.0.html

Cookie-free, privacy-friendly web analytics with Supermetry Cloud or your own self-hosted Supermetry server.

== Description ==

Supermetry is open-source web analytics that needs no cookies and stores no personal data. This plugin connects a WordPress site to Supermetry Cloud or to a self-hosted Supermetry server.

* Adds the Supermetry tracker to every public page (deferred).
* Leaves out logged-in users with the roles you choose (administrators by default).
* Lets you turn off outbound link, file download and form tracking on this site.
* Optional first-party proxy: the tracker and its events go through your own domain (`/wp-json/sm/v1/`), so ad blockers that block the Supermetry domain do not stop them.
* WooCommerce: sends one Purchase event with the order total and currency for every order, from your server, so ad blockers cannot drop it. Works with High-Performance Order Storage.

= External service =

This plugin sends page views and events to the Supermetry server you configure (Supermetry Cloud at https://app.supermetry.com, or your own server). Nothing is sent until you enter a site ID.

* Visitors' browsers load the tracker from that server (or from your site, with the proxy on) and send it page views and events: page URL, referrer, screen width and event properties.
* With the proxy on, and for WooCommerce purchases, your server forwards the visitor's User-Agent, IP address, language and privacy signals (Do Not Track, Global Privacy Control) so Supermetry can count unique visitors and respect those signals. Supermetry does not store IP addresses; it derives a daily-rotating visitor hash from them.
* Purchase events contain the order number, item count, coupon codes, total and currency, never names, addresses or e-mail addresses.

Supermetry Cloud is operated by Supermetry (https://supermetry.com), whose terms and privacy policy apply. Self-hosted servers are under your own control.

== Installation ==

1. Install and activate the plugin.
2. In Supermetry, add your site and copy its site ID (Settings → Installation).
3. In WordPress, go to Settings → Supermetry, enter the Supermetry URL (https://app.supermetry.com for Cloud) and the site ID, and save.
4. Open your site in a private window: the visit shows up in Supermetry within a few seconds.

= First-party proxy =

Turn on "First-party proxy" under Settings → Supermetry → Ad blockers.

* Supermetry Cloud: create the site's proxy key in Supermetry (Settings → Installation → Ad blockers) and paste it into "Proxy key". Without it, every proxied visitor appears to come from your server.
* Self-hosted: add your WordPress server's address to `SUPERMETRY_TRUSTED_PROXIES` on the Supermetry server.
* Behind Cloudflare or a load balancer, choose where the visitor address comes from ("Visitor address").
* Session replay uploads also go through the proxy; this needs pretty permalinks.

= WooCommerce =

Purchases are sent when the order-received ("thank you") page is shown, once per order. The order currency must be the Supermetry site's currency. Sites with "strict origin" in Supermetry accept these server-side events because the plugin sends your site's origin.

== Frequently Asked Questions ==

= Do I need a cookie banner? =

Supermetry sets no cookies and stores nothing on the visitor's device except an opt-out flag when a visitor opts out. Check with your own legal advisor for your jurisdiction.

= Can I send custom events? =

Yes. The tracker exposes `window.supermetry`:

`supermetry.track("Signup", { plan: "pro" });`

= Can I change the script tag? =

Use the `supermetry_script_attributes` filter, for example to add `data-consent="required"`. `supermetry_should_track` decides whether a request is tracked, `supermetry_script_dir` changes the script directory for servers with a custom `SUPERMETRY_TRACKER_PATH`, and `supermetry_woocommerce_purchase_props` changes the Purchase event properties.

== Changelog ==

= 0.1.0 =
* First release: tracker, role exclusions, automatic event switches, first-party proxy, WooCommerce purchases.
