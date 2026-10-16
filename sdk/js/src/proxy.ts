import { DEFAULT_HOST } from "./host";

export interface ProxyOptions {
  /** Supermetry origin to forward to. Defaults to Supermetry Cloud. */
  host?: string;
  getClientIp?: (request: Request) => string | null | undefined;
  fetch?: typeof fetch;
}

export type ProxyHandler = (request: Request) => Promise<Response>;

const eventsPath = "/api/public/v1/events";
const forwardedRequestHeaders = ["user-agent", "content-type", "origin", "referer", "dnt", "sec-gpc", "accept-language"];
const forwardedResponseHeaders = ["content-type", "cache-control", "etag", "last-modified", "retry-after"];

export function clientIp(request: Request): string | undefined {
  const forwarded = request.headers.get("x-forwarded-for")?.split(",")[0]?.trim();
  return forwarded || request.headers.get("x-real-ip")?.trim() || undefined;
}

function copyHeaders(from: Headers, names: string[]): Headers {
  const headers = new Headers();
  for (const name of names) {
    const value = from.get(name);
    if (value !== null) headers.set(name, value);
  }
  return headers;
}

export function createProxyHandler(options: ProxyOptions = {}): ProxyHandler {
  const host = (options.host || DEFAULT_HOST).replace(/\/+$/, "");
  const send = options.fetch ?? fetch;
  const getClientIp = options.getClientIp ?? clientIp;

  return async (request) => {
    const { pathname, search } = new URL(request.url);
    const isEvent = request.method === "POST" && pathname.endsWith(eventsPath);
    const isScript = (request.method === "GET" || request.method === "HEAD") && /\/js\/[\w.-]+\.js$/.test(pathname);
    if (!isEvent && !isScript) return new Response("Not Found", { status: 404 });

    const target = isEvent ? `${host}${eventsPath}` : `${host}${pathname.slice(pathname.lastIndexOf("/js/"))}${search}`;
    const headers = copyHeaders(request.headers, forwardedRequestHeaders);
    const ip = getClientIp(request);
    if (ip) headers.set("x-forwarded-for", ip);

    try {
      const upstream = await send(target, {
        method: request.method,
        headers,
        body: isEvent ? await request.text() : undefined,
        cache: isEvent ? "no-store" : undefined,
        redirect: "manual",
      });
      const responseHeaders = copyHeaders(upstream.headers, forwardedResponseHeaders);
      if (isEvent) responseHeaders.set("cache-control", "no-store");
      return new Response(request.method === "HEAD" ? null : upstream.body, {
        status: upstream.status,
        headers: responseHeaders,
      });
    } catch {
      return new Response("Bad Gateway", { status: 502, headers: { "cache-control": "no-store" } });
    }
  };
}
