import createClient, { type Middleware } from "openapi-fetch";

import type { paths } from "./schema";

export type { components, operations, paths } from "./schema";

export type PanelClientOptions = {
  apiKey: string;
  baseUrl: string;
  fetch?: typeof globalThis.fetch;
};

/** Creates a typed client for Panel's versioned public API. */
export function createPanelClient(options: PanelClientOptions) {
  const apiKey = options.apiKey.trim();
  if (!apiKey) throw new Error("apiKey is required");
  const client = createClient<paths>({
    baseUrl: options.baseUrl.replace(/\/$/, ""),
    fetch: options.fetch,
  });
  const authentication: Middleware = {
    onRequest({ request }) {
      request.headers.set("Authorization", `Bearer ${apiKey}`);
      return request;
    },
  };
  client.use(authentication);
  return client;
}

export type PanelClient = ReturnType<typeof createPanelClient>;
