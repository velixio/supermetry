import { register } from "@shopify/web-pixels-extension";

import { SUBSCRIBED_EVENTS, toRequest } from "./mapping.js";

// Web pixels run in a sandbox without access to the page, so the Supermetry
// tracker cannot run here: events go straight to the events endpoint.
register(({ analytics, settings }) => {
  for (const name of SUBSCRIBED_EVENTS) {
    analytics.subscribe(name, (event) => {
      const request = toRequest(event, settings);
      if (request) fetch(request.url, request.init).catch(() => {});
    });
  }
});
