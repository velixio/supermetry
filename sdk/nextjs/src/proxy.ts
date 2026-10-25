import { createProxyHandler, type ProxyOptions } from "@supermetry/tracker/proxy";

export type { ProxyOptions } from "@supermetry/tracker/proxy";

export interface SupermetryProxy {
  GET: (request: Request) => Promise<Response>;
  HEAD: (request: Request) => Promise<Response>;
  POST: (request: Request) => Promise<Response>;
  /** Session replay stylesheet uploads. */
  PUT: (request: Request) => Promise<Response>;
}

export function createSupermetryProxy(options: ProxyOptions = {}): SupermetryProxy {
  const handler = createProxyHandler(options);
  return { GET: handler, HEAD: handler, POST: handler, PUT: handler };
}
