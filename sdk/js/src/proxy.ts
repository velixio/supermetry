import { DEFAULT_HOST } from "./host";

export interface ProxyOptions {
  /** Supermetry origin to forward to. Defaults to Supermetry Cloud. */
  host?: string;
  getClientIp?: (request: Request) => string | null | undefined;
  /**
   * Supermetry Cloud first-party proxy key for this site (`smpk_…`). With it,
   * tracking requests carry the visitor's address so Cloud counts real
   * visitors instead of your server. Keep it server-side; never expose it.
   */
  proxyKey?: string;
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

// Upload caps, comfortably above core's own (events 8 KiB, replay start 4 KiB,
// chunk 256 KiB, stylesheet 1 MiB) so the proxy never refuses what core accepts.
const maxEventBytes = 64 * 1024;
const maxReplayBytes = 2 * 1024 * 1024;

class TooLarge extends Error {}

// Reads at most max bytes from the body, counting while streaming so a missing
// or false Content-Length cannot make the proxy buffer more.
async function readLimited(request: Request, max: number): Promise<Uint8Array> {
  const declared = Number(request.headers.get("content-length"));
  if (declared > max) throw new TooLarge();
  if (!request.body) return new Uint8Array(0);
  const parts: Uint8Array[] = [];
  let total = 0;
  const reader = request.body.getReader();
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    total += value.byteLength;
    if (total > max) {
      await reader.cancel().catch(() => undefined);
      throw new TooLarge();
    }
    parts.push(value);
  }
  const out = new Uint8Array(total);
  let offset = 0;
  for (const part of parts) {
    out.set(part, offset);
    offset += part.byteLength;
  }
  return out;
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
    const read = request.method === "GET" || request.method === "HEAD";
    const isEvent = request.method === "POST" && pathname.endsWith(eventsPath);
    const isScript = read && /\/js\/[\w.-]+\.js$/.test(pathname);
    // Session replay: the recorder script and its uploads, kept next to the
    // events endpoint (so under the same path prefix).
    const isRecorder = read && pathname.endsWith("/replay.js");
    const replay = pathname.match(/\/api\/replay\/(start|chunk|css\/[0-9a-f]+)$/);
    const isReplay = replay !== null && (replay[1].startsWith("css/") ? request.method === "PUT" : request.method === "POST");
    if (!isEvent && !isScript && !isRecorder && !isReplay) return new Response("Not Found", { status: 404 });

    const target = isEvent
      ? `${host}${eventsPath}`
      : isScript
        ? `${host}${pathname.slice(pathname.lastIndexOf("/js/"))}${search}`
        : isRecorder
          ? `${host}/replay.js${search}`
          : `${host}/api/replay/${replay![1]}${search}`;
    const upload = isEvent || isReplay;
    let body: string | ArrayBuffer | undefined;
    if (upload) {
      const max = isEvent || replay![1] === "start" ? maxEventBytes : maxReplayBytes;
      try {
        const bytes = await readLimited(request, max);
        body = isEvent ? new TextDecoder().decode(bytes) : (bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer);
      } catch (err) {
        if (!(err instanceof TooLarge)) throw err;
        return new Response("Payload Too Large", { status: 413, headers: { "cache-control": "no-store" } });
      }
    }
    const headers = copyHeaders(request.headers, forwardedRequestHeaders);
    const ip = getClientIp(request);
    if (ip) headers.set("x-forwarded-for", ip);
    if (options.proxyKey && ip && upload) {
      headers.set("x-supermetry-proxy-key", options.proxyKey);
      headers.set("x-supermetry-client-ip", ip);
    }

    try {
      const upstream = await send(target, {
        method: request.method,
        headers,
        body,
        cache: upload ? "no-store" : undefined,
        redirect: "manual",
      });
      const responseHeaders = copyHeaders(upstream.headers, forwardedResponseHeaders);
      if (upload) responseHeaders.set("cache-control", "no-store");
      return new Response(request.method === "HEAD" ? null : upstream.body, {
        status: upstream.status,
        headers: responseHeaders,
      });
    } catch {
      return new Response("Bad Gateway", { status: 502, headers: { "cache-control": "no-store" } });
    }
  };
}
