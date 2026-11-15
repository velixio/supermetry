import type { Transport, TransportRequest } from "../src/types";

export interface Recorded {
  headers: Record<string, string>;
  body: string;
  parsed: { site: string; events: Record<string, any>[] };
}

/**
 * In-process fake of POST /api/public/v1/app-events enforcing the wire contract:
 * body <= 16 KB, 1..20 events, X-Supermetry-Key, site match, 202 reply with drops.
 * `script` lets a test force statuses (or a thrown network error) for the next requests.
 */
export class FakeServer {
  key = "k_test";
  site = "app_1";
  requests: Recorded[] = [];
  script: Array<number | "network"> = [];
  seen = new Set<string>();
  accepted: Record<string, any>[] = [];
  /** When set, the next accepted request drops every event with this reason. */
  forceDrop = "";

  transport: Transport = async (req: TransportRequest) => {
    const body = req.body;
    this.requests.push({ headers: req.headers, body, parsed: safeParse(body) });
    const next = this.script.shift();
    if (next === "network") throw new TypeError("fetch failed");
    if (typeof next === "number") return { status: next };
    if (new TextEncoder().encode(body).length > 16384) return { status: 400 };
    const p = safeParse(body);
    if (!p || !Array.isArray(p.events) || p.events.length < 1 || p.events.length > 20) return { status: 400 };
    if (req.headers["X-Supermetry-Key"] !== this.key || p.site !== this.site) return { status: 401 };
    const dropped: { index: number; reason: string }[] = [];
    p.events.forEach((e: any, index: number) => {
      if (this.forceDrop) dropped.push({ index, reason: this.forceDrop });
      else if (this.seen.has(e.id)) dropped.push({ index, reason: "duplicate" });
      else {
        this.seen.add(e.id);
        this.accepted.push(e);
      }
    });
    return { status: 202, body: JSON.stringify({ accepted: p.events.length - dropped.length, dropped }) };
  };
}

function safeParse(s: string): any {
  try {
    return JSON.parse(s);
  } catch {
    return null;
  }
}
