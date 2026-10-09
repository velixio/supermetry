# `@supermetry/tracker`

Typed browser wrapper for Supermetry's full tracking script. It injects the script; it does not bundle the tracker itself.

```ts
import { init } from "@supermetry/tracker";

const supermetry = init({
  site: "your-site-id",
  src: "https://analytics.example.com/js/script.full.js",
  api: "https://analytics.example.com/api/public/v1/events",
  hash: true,
  beforeSend: (event) => ({
    ...event,
    props: { ...event.props, app: "storefront" },
  }),
});

supermetry.pageview();
supermetry.track("Signup", { plan: "pro" });
```

`site` and `src` are required. The optional `manual`, `hash`, `consent`, `forms`, `outbound`, `downloads`, and `allowLocalhost` settings configure the corresponding tracker data attributes. When `consent` is enabled, resolve it after the visitor chooses:

```ts
supermetry.consent(true);
```

Calls made while the script is loading are queued in memory. Repeated `init` calls in the same page return the original tracker. During server rendering, `init` returns safe no-op methods and does not access the DOM.
