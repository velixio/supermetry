import { createProxyHandler, type ProxyOptions } from "@supermetry/tracker/proxy";

export type { ProxyOptions } from "@supermetry/tracker/proxy";

export interface ProxyArgs {
  request: Request;
}

export interface SupermetryProxy {
  loader: (args: ProxyArgs) => Promise<Response>;
  action: (args: ProxyArgs) => Promise<Response>;
}

export function createSupermetryProxy(options: ProxyOptions = {}): SupermetryProxy {
  const handler = createProxyHandler(options);
  const route = ({ request }: ProxyArgs) => handler(request);
  return { loader: route, action: route };
}
