import createClient, { type Middleware } from "openapi-fetch";

import type { paths } from "./schema";

export type { components, operations, paths } from "./schema";

/** Supermetry Cloud, used when `baseUrl` is omitted. */
export const DEFAULT_BASE_URL = "https://app.supermetry.com";

export type SupermetryClientOptions = {
  apiKey: string;
  /** Supermetry origin. Defaults to Supermetry Cloud; set it when self-hosting. */
  baseUrl?: string;
  fetch?: typeof globalThis.fetch;
};

/** Creates a typed client for Supermetry's versioned public API. */
export function createSupermetryClient(options: SupermetryClientOptions) {
  const apiKey = options.apiKey.trim();
  if (!apiKey) throw new Error("apiKey is required");
  const client = createClient<paths>({
    baseUrl: (options.baseUrl || DEFAULT_BASE_URL).replace(/\/$/, ""),
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

export type SupermetryClient = ReturnType<typeof createSupermetryClient>;
