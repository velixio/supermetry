import { createServer } from "node:http";
import { normalizeConfig, toEvents, toRequest } from "./mapping.js";
import { verifySignature } from "./signature.js";

const MAX_BODY = 1024 * 1024;

// Returns a request handler. Ghost retries non-2xx responses, and event IDs
// are stable per member/subscription, so a retry never double counts.
export function createRelay(options) {
  const config = normalizeConfig(options);
  const secret = String(options.secret ?? "");
  if (!secret) throw new Error("GHOST_WEBHOOK_SECRET is required");
  const path = options.path || "/ghost";
  const toleranceMs = Number(options.toleranceMs ?? 5 * 60 * 1000);
  const fetchImpl = options.fetch ?? globalThis.fetch;
  const log = options.log ?? console;
  const now = options.now ?? Date.now;

  return async function handle(req, res) {
    const url = new URL(req.url ?? "/", "http://relay.invalid");
    if (req.method === "GET" && url.pathname === "/healthz") return reply(res, 200, "ok");
    if (url.pathname !== path) return reply(res, 404, "not found");
    if (req.method !== "POST") return reply(res, 405, "method not allowed", { Allow: "POST" });

    let raw;
    try {
      raw = await readBody(req);
    } catch {
      return reply(res, 413, "payload too large");
    }
    if (!verifySignature(raw, req.headers["x-ghost-signature"], secret, { now: now(), toleranceMs })) {
      return reply(res, 401, "invalid signature");
    }

    let body;
    try {
      body = JSON.parse(raw.toString("utf8"));
    } catch {
      return reply(res, 400, "invalid json");
    }

    const events = toEvents(body, config, { now: now() });
    try {
      for (const event of events) {
        const { url: endpoint, init } = toRequest(event, config);
        const response = await fetchImpl(endpoint, { ...init, signal: AbortSignal.timeout(5000) });
        if (response.status >= 500 || response.status === 429) throw new Error(`Supermetry responded ${response.status}`);
        if (!response.ok) log.warn?.(`supermetry: ${event.name} rejected with ${response.status}`);
      }
    } catch (error) {
      log.error?.(`supermetry: ${error.message}`);
      return reply(res, 502, "upstream error");
    }
    return reply(res, 202, `sent ${events.length}`);
  };
}

export function createRelayServer(options) {
  const handle = createRelay(options);
  return createServer((req, res) => {
    handle(req, res).catch(() => reply(res, 500, "error"));
  });
}

function readBody(req) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    req.on("data", (chunk) => {
      size += chunk.length;
      if (size > MAX_BODY) {
        reject(new Error("too large"));
        req.destroy();
        return;
      }
      chunks.push(chunk);
    });
    req.on("end", () => resolve(Buffer.concat(chunks)));
    req.on("error", reject);
  });
}

function reply(res, status, text, headers = {}) {
  if (res.headersSent) return;
  res.writeHead(status, { "Content-Type": "text/plain; charset=utf-8", ...headers });
  res.end(text);
}
