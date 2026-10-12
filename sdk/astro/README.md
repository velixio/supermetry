# `@supermetry/astro`

Astro component for [Supermetry](https://github.com/supermetry) analytics (Astro 4+). It renders the tracker `<script>` tag; nothing is bundled into your pages.

```sh
npm install @supermetry/astro
```

## Quick start

```astro
---
// src/layouts/Layout.astro
import Supermetry from "@supermetry/astro";
---

<html lang="en">
  <head>
    <Supermetry site="your-site-id" host="https://analytics.example.com" />
  </head>
  <body><slot /></body>
</html>
```

### View Transitions

With `<ClientRouter />` (or `<ViewTransitions />` in Astro 4), pass `viewTransitions`. The tracker then runs in manual mode, persists across navigations, and sends a pageview on every `astro:page-load`, including the first.

```astro
<Supermetry site="your-site-id" host="https://analytics.example.com" viewTransitions />
```

### Props

| Prop | |
|---|---|
| `site` | Required public site ID. |
| `host` | Supermetry origin. Defaults to `""`: `/js/script.js` and `/api/public/v1/events` on your own domain (proxy them, or use `src` / `api`). |
| `src`, `api` | Explicit script URL and events endpoint. |
| `manual`, `hash`, `forms`, `outbound`, `downloads`, `profiles`, `allowLocalhost` | Tracker data attributes. |
| `consent` | Wait for `window.supermetry.consent(true)` before sending. |
| `beforeSend` | Name of a global function used as the tracker's `beforeSend` hook. |
| `viewTransitions` | See above. |

Send custom events from client scripts through the tracker global:

```html
<script>
  window.supermetry?.track("Signup", { plan: "pro" });
</script>
```

or install `@supermetry/tracker` and use its queued, typed wrapper: `getTracker().track(...)`.
