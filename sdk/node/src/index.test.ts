import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { createServer, type IncomingHttpHeaders, type Server } from "node:http";
import type { AddressInfo } from "node:net";

import { DEFAULT_USER_AGENT, Supermetry, type EventPayload, type SupermetryError } from "./index";
import { sanitizeProps, sanitizeRevenue, sanitizeTraits } from "./validate";

interface Captured {
  url: string;
  init: RequestInit;
  body: EventPayload;
  headers: Record<string, string>;
}

function fakeFetch(statuses: Array<number | Error> = [202]) {
  const calls: Captured[] = [];
  const fn = (async (url: string | URL | Request, init?: RequestInit) => {
    const headers = init?.headers as Record<string, string>;
    calls.push({ url: String(url), init: init!, body: JSON.parse(String(init?.body)), headers });
    const next = statuses.length > 1 ? statuses.shift()! : statuses[0]!;
    if (next instanceof Error) throw next;
    return new Response(null, { status: next, headers: next === 429 ? { "Retry-After": "0" } : {} });
  }) as typeof fetch;
  return { fn, calls };
}

function client(fetchImpl: typeof fetch, extra: Partial<ConstructorParameters<typeof Supermetry>[0]> = {}) {
  return new Supermetry({ site: "site_1", host: "https://a.supermetry.com/", fetch: fetchImpl, ...extra });
}

describe("payload", () => {
  test("track builds a v1 event with id, ts and endpoint", async () => {
    const { fn, calls } = fakeFetch();
    const sm = client(fn);
    const before = Date.now();
    const result = await sm.track("Signup", {
      url: "https://shop.supermetry.com/pricing",
      props: { plan: "pro", seats: 3, trial: true },
      revenue: { amount: 49.9, currency: "USD" },
      profileId: "user_42",
      referrer: "https://google.com/",
    });
    expect(result).toEqual({ ok: true, status: 202 });
    expect(calls).toHaveLength(1);
    const { url, body, init, headers } = calls[0]!;
    expect(url).toBe("https://a.supermetry.com/api/public/v1/events");
    expect(init.method).toBe("POST");
    expect(headers["Content-Type"]).toBe("application/json");
    expect(headers["User-Agent"]).toBe(DEFAULT_USER_AGENT);
    expect(body.v).toBe(1);
    expect(body.id).toMatch(/^[0-9a-f]{32}$/);
    expect(body.ts).toBeGreaterThanOrEqual(before);
    expect(body).toMatchObject({
      site: "site_1",
      name: "Signup",
      url: "https://shop.supermetry.com/pricing",
      referrer: "https://google.com/",
      props: { plan: "pro", seats: 3, trial: true },
      revenue: { amount: 49.9, currency: "USD" },
      profile_id: "user_42",
    });
    expect(body.traits).toBeUndefined();
  });

  test("pageview uses default url, resolves relative urls and drops revenue", async () => {
    const { fn, calls } = fakeFetch();
    const sm = client(fn, { url: "https://shop.supermetry.com/" });
    await sm.pageview();
    await sm.pageview({ url: "/docs?a=1" });
    await sm.track("pageview", { url: "/x", revenue: { amount: 1 } });
    expect(calls.map((c) => c.body.url)).toEqual([
      "https://shop.supermetry.com/",
      "https://shop.supermetry.com/docs?a=1",
      "https://shop.supermetry.com/x",
    ]);
    expect(calls.every((c) => c.body.name === "pageview" && c.body.revenue === undefined)).toBe(true);
  });

  test("identify sends profile_id and only valid traits", async () => {
    const { fn, calls } = fakeFetch();
    const sm = client(fn, { url: "https://shop.supermetry.com/" });
    await sm.identify("user_42", { email: "a@b.co", plan: 2, bad: { x: 1 } as never, ["k".repeat(65)]: "x" });
    expect(calls[0]!.body).toMatchObject({ name: "identify", profile_id: "user_42", traits: { email: "a@b.co", plan: "2" } });
    expect(Object.keys(calls[0]!.body.traits!)).toEqual(["email", "plan"]);
  });

  test("identify does not store the profile ID on the shared client", async () => {
    const { fn, calls } = fakeFetch();
    const sm = client(fn, { url: "https://shop.supermetry.com/" });
    await sm.identify("user_42");
    await sm.track("Other visitor");
    expect(calls[1]!.body.profile_id).toBeUndefined();
  });

  test("traits never leak onto non-identify events", async () => {
    const { fn, calls } = fakeFetch();
    const sm = client(fn, { url: "https://shop.supermetry.com/" });
    await sm.track("Click", { props: { a: "1" }, traits: { x: "y" } } as never);
    expect(calls[0]!.body.traits).toBeUndefined();
  });

  test("forwards visitor UA and IP, optional Origin", async () => {
    const { fn, calls } = fakeFetch();
    const sm = client(fn, { sendOrigin: true });
    await sm.pageview({ url: "https://shop.supermetry.com/a", userAgent: "Mozilla/5.0 Visitor", ip: "203.0.113.9" });
    expect(calls[0]!.headers["User-Agent"]).toBe("Mozilla/5.0 Visitor");
    expect(calls[0]!.headers["X-Forwarded-For"]).toBe("203.0.113.9");
    expect(calls[0]!.headers["Origin"]).toBe("https://shop.supermetry.com");
  });

  test("custom userAgent replaces the default", async () => {
    const { fn, calls } = fakeFetch();
    await client(fn, { userAgent: "my-app/1.0" }).pageview({ url: "https://x.supermetry.com/" });
    expect(calls[0]!.headers["User-Agent"]).toBe("my-app/1.0");
  });
});

describe("validation and errors", () => {
  test("invalid url or name reports to onError and never throws", async () => {
    const { fn, calls } = fakeFetch();
    const errors: SupermetryError[] = [];
    const sm = client(fn, { onError: (e) => errors.push(e) });
    expect((await sm.pageview()).ok).toBe(false);
    expect((await sm.track("x", { url: "ftp://x.supermetry.com" })).ok).toBe(false);
    expect((await sm.track("x", { url: "https://u:p@example.com" })).ok).toBe(false);
    expect((await sm.track("", { url: "https://x.supermetry.com" })).ok).toBe(false);
    expect((await sm.identify("", {}, { url: "https://x.supermetry.com" })).ok).toBe(false);
    expect((await sm.track("identify", { url: "https://x.supermetry.com" })).ok).toBe(false);
    expect(calls).toHaveLength(0);
    expect(errors).toHaveLength(6);
  });

  test("oversized body is rejected locally", async () => {
    const { fn, calls } = fakeFetch();
    const errors: unknown[] = [];
    const props = Object.fromEntries(Array.from({ length: 10 }, (_, i) => [`k${i}`, "x".repeat(1500)]));
    await client(fn, { onError: (e) => errors.push(e) }).track("Big", { url: "https://x.supermetry.com", props });
    expect(calls).toHaveLength(0);
    expect(errors).toHaveLength(1);
  });

  test("throwing onError is swallowed", async () => {
    const { fn } = fakeFetch([400]);
    const sm = client(fn, { onError: () => { throw new Error("boom"); } });
    expect((await sm.pageview({ url: "https://x.supermetry.com" })).status).toBe(400);
  });

  test("disabled sends nothing", async () => {
    const { fn, calls } = fakeFetch();
    await client(fn, { disabled: true }).pageview({ url: "https://x.supermetry.com" });
    expect(calls).toHaveLength(0);
  });

  test("constructor requires site and a non-empty host", () => {
    expect(() => new Supermetry({ site: "", host: "https://a" })).toThrow();
    expect(() => new Supermetry({ site: "s", host: "" })).toThrow();
  });

  test("host defaults to Supermetry Cloud", () => {
    expect(new Supermetry({ site: "s" }).endpoint).toBe("https://app.supermetry.com/api/public/v1/events");
  });
});

describe("retries", () => {
  for (const failure of [503, 429, new TypeError("fetch failed")]) {
    test(`retries once with the same id after ${failure instanceof Error ? "network error" : failure}`, async () => {
      const { fn, calls } = fakeFetch([failure, 202]);
      const result = await client(fn).pageview({ url: "https://x.supermetry.com" });
      expect(result.ok).toBe(true);
      expect(calls).toHaveLength(2);
      expect(calls[1]!.body.id).toBe(calls[0]!.body.id);
      expect(calls[1]!.body.ts).toBe(calls[0]!.body.ts);
    });
  }

  test("gives up after the retry and reports", async () => {
    const { fn, calls } = fakeFetch([500]);
    const errors: SupermetryError[] = [];
    const result = await client(fn, { onError: (e) => errors.push(e) }).pageview({ url: "https://x.supermetry.com" });
    expect(result).toMatchObject({ ok: false, status: 500 });
    expect(calls).toHaveLength(2);
    expect(errors[0]!.status).toBe(500);
  });

  test("does not retry 400", async () => {
    const { fn, calls } = fakeFetch([400]);
    await client(fn).pageview({ url: "https://x.supermetry.com" });
    expect(calls).toHaveLength(1);
  });

  test("retry: false sends once", async () => {
    const { fn, calls } = fakeFetch([503]);
    await client(fn, { retry: false }).pageview({ url: "https://x.supermetry.com" });
    expect(calls).toHaveLength(1);
  });
});

describe("flush and close", () => {
  test("flush awaits fire-and-forget sends; close stops new events", async () => {
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    let sent = 0;
    const fn = (async () => {
      await gate;
      sent++;
      return new Response(null, { status: 202 });
    }) as unknown as typeof fetch;
    const sm = client(fn, { url: "https://x.supermetry.com/" });
    sm.pageview();
    sm.track("A");
    const flushed = sm.flush();
    expect(sent).toBe(0);
    release();
    await flushed;
    expect(sent).toBe(2);
    await sm.close();
    expect((await sm.pageview()).ok).toBe(false);
    expect(sent).toBe(2);
  });
});

describe("sanitizers", () => {
  test("props: primitives only, 30 keys max, long values truncated", () => {
    const many = Object.fromEntries(Array.from({ length: 40 }, (_, i) => [`k${i}`, i]));
    expect(Object.keys(sanitizeProps(many)!)).toHaveLength(30);
    const props = sanitizeProps({ a: "x", b: null, c: { d: 1 } as never, e: Number.NaN, "": "y", f: "é".repeat(1500) })!;
    expect(Object.keys(props)).toEqual(["a", "f"]);
    expect(new TextEncoder().encode(String(props.f)).length).toBeLessThanOrEqual(2000);
    expect(sanitizeProps({})).toBeUndefined();
  });

  test("traits: max 20, value <= 256 chars", () => {
    const many = Object.fromEntries(Array.from({ length: 25 }, (_, i) => [`k${i}`, "v"]));
    expect(Object.keys(sanitizeTraits(many)!)).toHaveLength(20);
    expect(sanitizeTraits({ a: "x".repeat(257) })).toBeUndefined();
  });

  test("revenue", () => {
    expect(sanitizeRevenue({ amount: "49.90", currency: "EUR" })).toEqual({ amount: "49.90", currency: "EUR" });
    expect(sanitizeRevenue({ amount: 0.1 + 0.2 })).toEqual({ amount: 0.3 });
    expect(sanitizeRevenue({ amount: "1.1234567" })).toBeUndefined();
    expect(sanitizeRevenue({ amount: -1 })).toBeUndefined();
    expect(sanitizeRevenue({ amount: 1, currency: "usd" })).toBeUndefined();
  });
});

describe("live HTTP", () => {
  let server: Server;
  let base: string;
  const received: Array<{ method?: string; path?: string; headers: IncomingHttpHeaders; body: string }> = [];

  beforeAll(async () => {
    server = createServer((req, res) => {
      let body = "";
      req.on("data", (chunk) => (body += chunk));
      req.on("end", () => {
        received.push({ method: req.method, path: req.url, headers: req.headers, body });
        res.writeHead(received.length === 1 ? 503 : 202).end();
      });
    });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  });

  afterAll(() => {
    server.close();
  });

  test("real fetch sends the contract headers and body, retrying 503", async () => {
    const sm = new Supermetry({ site: "site_live", host: base });
    sm.track("Purchase", {
      url: "https://shop.supermetry.com/checkout",
      props: { sku: "A1", qty: 2 },
      revenue: { amount: "19.99", currency: "USD" },
      profileId: "user_1",
      userAgent: "Mozilla/5.0 (X11; Linux x86_64) Firefox/131.0",
      ip: "198.51.100.7",
    });
    await sm.flush();

    expect(received).toHaveLength(2);
    for (const request of received) {
      expect(request.method).toBe("POST");
      expect(request.path).toBe("/api/public/v1/events");
      expect(request.headers["content-type"]).toBe("application/json");
      expect(request.headers["user-agent"]).toBe("Mozilla/5.0 (X11; Linux x86_64) Firefox/131.0");
      expect(request.headers["x-forwarded-for"]).toBe("198.51.100.7");
    }
    const first = JSON.parse(received[0]!.body);
    const second = JSON.parse(received[1]!.body);
    expect(second).toEqual(first);
    expect(Object.keys(first).sort()).toEqual(
      ["id", "name", "profile_id", "props", "revenue", "site", "ts", "url", "v"],
    );
    expect(first).toMatchObject({
      v: 1,
      site: "site_live",
      name: "Purchase",
      url: "https://shop.supermetry.com/checkout",
      props: { sku: "A1", qty: 2 },
      revenue: { amount: "19.99", currency: "USD" },
      profile_id: "user_1",
    });
    expect(first.id).toMatch(/^[A-Za-z0-9_-]{1,64}$/);

    received.length = 0;
    await sm.pageview({ url: "https://shop.supermetry.com/" });
    expect(received[0]!.headers["user-agent"]).toBe(DEFAULT_USER_AGENT);
    expect(received[0]!.headers["x-forwarded-for"]).toBeUndefined();
  });
});
