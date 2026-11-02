# Ad blockers

Some visitors run ad blockers or privacy extensions that stop analytics requests. This guide explains what Supermetry already does about it and what you can add so those visitors are counted too.

## What Supermetry does by default

You don't need to change anything for these:

- **Neutral, per-site URLs.** Your site's script sends events to `/e/<site ID>` rather than a well-known analytics path such as `/api/.../events`. Session replay uses the same base: `/e/<site ID>/r.js`, `/s`, `/c` and `/k/...`. Most filter rules are written for well-known paths and don't match these.
- **Ordinary requests.** Events go out as normal `fetch` requests (with `keepalive`, so the last event before a page closes still arrives), not as `sendBeacon` "ping" requests, which many filter lists block outright.
- **Short module names.** Optional modules load as `?m=v,h,r`, so the script URL never contains words like `replay` or `heatmap`.

Some blockers still block requests to the Supermetry domain itself (`app.supermetry.com` on Supermetry Cloud, or your own Supermetry host), whatever the path. To count those visitors, use one of the options below.

## Check whether you are affected

1. Open your site in a browser with your ad blocker turned on.
2. Open the developer tools, select the **Network** tab and reload the page.
3. Look for the request to `/e/<site ID>`. If it shows `blocked`, or the console shows `net::ERR_BLOCKED_BY_CLIENT`, the blocker is stopping it.

With uBlock Origin you can see the exact rule: open the uBlock menu, then the **Logger** (list icon), and reload your site. Blocked requests are shown in red, with the filter and the list it came from.

## Option 1: Proxy through your own domain (recommended)

Your own web server forwards the tracking requests to Supermetry. The browser then only talks to your domain, which blockers have no reason to block. Your pages load the script from `/sm/js/<site ID>.js` on your own domain.

The ready-made configuration, already filled in with your site ID and Supermetry host, is in the dashboard: **Settings → Installation → Ad blockers**. It covers nginx, Caddy, a Cloudflare Worker and Next.js.

### Steps

1. **Supermetry Cloud only: create the site's proxy key.** In **Settings → Installation → Ad blockers**, select **Create the proxy key**. Without the key, every visitor would appear to come from your server: one location for everyone, and visitors merged together. With the key, Cloud uses each visitor's own address.

   Keep the key on your server only: in the proxy's configuration, environment or secret store. Never put it in HTML, client-side JavaScript or a public repository.

2. **Add the proxy to your web server.** Forward these paths, with the `/sm` prefix removed, to your Supermetry host:

   | Path on your domain | Forwarded to |
   |---|---|
   | `/sm/js/...` | `/js/...` (the tracker script) |
   | `/sm/px/...` | `/px/...` (tracking pixels) |
   | `/sm/api/public/...` | `/api/public/...` (events) |
   | `/sm/replay.js` | `/replay.js` (session replay recorder) |
   | `/sm/api/replay/...` | `/api/replay/...` (session replay uploads) |

   On every forwarded request, the proxy should also:

   - set `Host` to your Supermetry host;
   - on Supermetry Cloud, send `X-Supermetry-Proxy-Key: <your key>` and `X-Supermetry-Client-IP: <the visitor's address>`. The proxy must always set the address itself, replacing anything the browser sent;
   - when self-hosting, send the visitor's address in `X-Forwarded-For`, and add the proxy's address to `SUPERMETRY_TRUSTED_PROXIES` on your Supermetry server (see [Configuration](configuration.md));
   - drop cookies (tracking needs none) and allow request bodies up to 2 MB (session replay uploads).

3. **Replace the snippet on your pages** with the first-party one:

   ```html
   <script defer src="/sm/js/YOUR_SITE_ID.js"></script>
   ```

   With the Next.js proxy, render `<Supermetry site="YOUR_SITE_ID" host="" />` instead.

4. **Check it:** reload your site with the ad blocker on. The script and the event requests should now go to your own domain and succeed.

If a proxy key is wrong, missing or revoked, tracking still works. Visitors are then counted as coming from your server until the key is fixed.

### nginx example (Supermetry Cloud)

```nginx
set $sm_key "smpk_REPLACE_WITH_YOUR_KEY";   # keep this file readable only by nginx

location ~ ^/sm/(js/|px/|api/public/|replay\.js$|api/replay/) {
    rewrite ^/sm/(.*)$ /$1 break;
    proxy_pass https://app.supermetry.com;
    proxy_ssl_server_name on;
    client_max_body_size 2m;
    proxy_set_header Host app.supermetry.com;
    proxy_set_header X-Supermetry-Proxy-Key $sm_key;
    proxy_set_header X-Supermetry-Client-IP $remote_addr;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header Cookie "";
}
```

When self-hosting, remove the two `X-Supermetry-*` lines and the `set` line, and use your own Supermetry host instead of `app.supermetry.com`.

If nginx itself sits behind a CDN or load balancer, `$remote_addr` is the CDN's address. Configure nginx's `real_ip` module (`set_real_ip_from` for the CDN's ranges and `real_ip_header` for its client header) so that `$remote_addr` is the visitor's address.

## Option 2: Send events from your server

Events your backend sends directly to Supermetry can't be blocked by anything in the browser. They do miss what only the browser knows: screen size, engagement time, scroll depth and session replay. That makes them a good addition to the script, not a replacement. For example, use them to record sign-ups and purchases reliably.

The dashboard has ready-made code for Node.js, Express, Python, Go and curl under **Settings → Installation → Install recipes → From your server**. See also the [API documentation](../api.md).

## Turning off the neutral URLs (self-hosted)

If a proxy or firewall in front of your Supermetry server forwards only specific paths, either forward `/e/` too, or set `SUPERMETRY_NEUTRAL_PATHS=false` so scripts keep the classic paths. See [Configuration](configuration.md#neutral-per-site-paths).
