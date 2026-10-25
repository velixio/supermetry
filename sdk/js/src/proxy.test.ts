import { describe, expect, test } from "bun:test";

import { createProxyHandler } from "./proxy";

interface Sent {
  url: string;
  init: RequestInit;
}

function upstream(response: () => Response): { sent: Sent[]; fetch: typeof fetch } {
  const sent: Sent[] = [];
  const fake = (async (url: string, init: RequestInit) => {
    sent.push({ url, init });
    return response();
  }) as unknown as typeof fetch;
  return { sent, fetch: fake };
}

describe("createProxyHandler", () => {
  test("defaults to Supermetry Cloud", async () => {
    const { sent, fetch } = upstream(() => new Response("", { status: 200 }));
    const handler = createProxyHandler({ fetch });

    await handler(new Request("https://shop.example.com/js/script.js"));

    expect(sent[0].url).toBe("https://app.supermetry.com/js/script.js");
  });

  test("forwards events with the visitor user agent and client IP, never cached", async () => {
    const { sent, fetch } = upstream(() => new Response(null, { status: 202, headers: { "cache-control": "max-age=60" } }));
    const handler = createProxyHandler({ host: "https://analytics.example.com/", fetch });
    const body = JSON.stringify({ v: 1, name: "pageview" });

    const response = await handler(
      new Request("https://shop.example.com/api/public/v1/events", {
        method: "POST",
        body,
        headers: {
          "content-type": "text/plain",
          "user-agent": "Mozilla/5.0 Test",
          "x-forwarded-for": "203.0.113.7, 10.0.0.1",
          cookie: "secret=1",
          origin: "https://shop.example.com",
        },
      }),
    );

    expect(response.status).toBe(202);
    expect(response.headers.get("cache-control")).toBe("no-store");
    expect(sent).toHaveLength(1);
    expect(sent[0]!.url).toBe("https://analytics.example.com/api/public/v1/events");
    expect(sent[0]!.init.method).toBe("POST");
    expect(sent[0]!.init.body).toBe(body);
    expect(sent[0]!.init.cache).toBe("no-store");
    const headers = sent[0]!.init.headers as Headers;
    expect(headers.get("user-agent")).toBe("Mozilla/5.0 Test");
    expect(headers.get("x-forwarded-for")).toBe("203.0.113.7");
    expect(headers.get("origin")).toBe("https://shop.example.com");
    expect(headers.get("cookie")).toBeNull();
  });

  test("passes Retry-After through on rate limits", async () => {
    const { fetch } = upstream(() => new Response("slow down", { status: 429, headers: { "retry-after": "30" } }));
    const handler = createProxyHandler({ host: "https://analytics.example.com", fetch, getClientIp: () => "198.51.100.1" });

    const response = await handler(new Request("https://shop.example.com/api/public/v1/events", { method: "POST", body: "{}" }));

    expect(response.status).toBe(429);
    expect(response.headers.get("retry-after")).toBe("30");
  });

  test("serves tracker scripts with upstream caching", async () => {
    const { sent, fetch } = upstream(
      () => new Response("(()=>{})()", { headers: { "content-type": "application/javascript", "cache-control": "public, max-age=3600" } }),
    );
    const handler = createProxyHandler({ host: "https://analytics.example.com", fetch });

    const response = await handler(new Request("https://shop.example.com/stats/js/script.full.js?v=1", { headers: { "x-real-ip": "192.0.2.4" } }));

    expect(await response.text()).toBe("(()=>{})()");
    expect(response.headers.get("cache-control")).toBe("public, max-age=3600");
    expect(sent[0]!.url).toBe("https://analytics.example.com/js/script.full.js?v=1");
    expect((sent[0]!.init.headers as Headers).get("x-forwarded-for")).toBe("192.0.2.4");
  });

  test("serves the replay recorder from a prefixed path", async () => {
    const { sent, fetch } = upstream(() => new Response("recorder", { headers: { "content-type": "application/javascript" } }));
    const handler = createProxyHandler({ host: "https://analytics.example.com", fetch });

    const response = await handler(new Request("https://shop.example.com/stats/replay.js"));

    expect(await response.text()).toBe("recorder");
    expect(sent[0]!.url).toBe("https://analytics.example.com/replay.js");
  });

  test("forwards replay uploads with their query, binary body and client IP, never cached", async () => {
    const { sent, fetch } = upstream(() => new Response(null, { status: 202 }));
    const handler = createProxyHandler({ host: "https://analytics.example.com", fetch, getClientIp: () => "203.0.113.9" });
    const bytes = new Uint8Array([31, 139, 8, 0, 255]);

    const start = await handler(new Request("https://shop.example.com/stats/api/replay/start", { method: "POST", body: "{}", headers: { "content-type": "application/json" } }));
    const chunk = await handler(new Request("https://shop.example.com/stats/api/replay/chunk?s=abc&n=1", { method: "POST", body: bytes, headers: { "content-type": "application/octet-stream" } }));
    const css = await handler(new Request("https://shop.example.com/stats/api/replay/css/0a1b2c?s=abc&c=1", { method: "PUT", body: bytes, headers: { "content-type": "application/gzip" } }));

    expect([start.status, chunk.status, css.status]).toEqual([202, 202, 202]);
    expect(chunk.headers.get("cache-control")).toBe("no-store");
    expect(sent.map((s) => `${s.init.method} ${s.url}`)).toEqual([
      "POST https://analytics.example.com/api/replay/start",
      "POST https://analytics.example.com/api/replay/chunk?s=abc&n=1",
      "PUT https://analytics.example.com/api/replay/css/0a1b2c?s=abc&c=1",
    ]);
    expect(new Uint8Array(sent[1]!.init.body as ArrayBuffer)).toEqual(bytes);
    expect((sent[2]!.init.headers as Headers).get("content-type")).toBe("application/gzip");
    expect((sent[1]!.init.headers as Headers).get("x-forwarded-for")).toBe("203.0.113.9");
  });

  test("refuses oversized uploads with 413 without forwarding them, even without Content-Length", async () => {
    const { sent, fetch } = upstream(() => new Response(null, { status: 202 }));
    const handler = createProxyHandler({ host: "https://analytics.example.com", fetch });
    const streamed = (bytes: number) =>
      new ReadableStream<Uint8Array>({
        start(controller) {
          for (let left = bytes; left > 0; left -= 65536) controller.enqueue(new Uint8Array(Math.min(65536, left)));
          controller.close();
        },
      });

    const chunk = await handler(new Request("https://shop.example.com/api/replay/chunk?s=abc", { method: "POST", body: streamed(2 * 1024 * 1024 + 1), duplex: "half" } as RequestInit));
    const event = await handler(new Request("https://shop.example.com/api/public/v1/events", { method: "POST", body: streamed(64 * 1024 + 1), duplex: "half" } as RequestInit));
    const ok = await handler(new Request("https://shop.example.com/api/replay/chunk?s=abc", { method: "POST", body: streamed(256 * 1024), duplex: "half" } as RequestInit));

    expect([chunk.status, event.status, ok.status]).toEqual([413, 413, 202]);
    expect(chunk.headers.get("cache-control")).toBe("no-store");
    expect(sent).toHaveLength(1);
  });

  test("sends the proxy key and visitor address on tracking requests when configured", async () => {
    const { sent, fetch } = upstream(() => new Response(null, { status: 202 }));
    const handler = createProxyHandler({ fetch, proxyKey: "smpk_test", getClientIp: () => "203.0.113.9" });

    await handler(new Request("https://shop.example.com/sm/api/public/v1/events", { method: "POST", body: "{}", headers: { "x-supermetry-client-ip": "198.51.100.66" } }));
    await handler(new Request("https://shop.example.com/sm/api/replay/start", { method: "POST", body: "{}" }));
    await handler(new Request("https://shop.example.com/sm/js/script.js"));

    const header = (i: number, name: string) => (sent[i]!.init.headers as Headers).get(name);
    expect([header(0, "x-supermetry-proxy-key"), header(0, "x-supermetry-client-ip")]).toEqual(["smpk_test", "203.0.113.9"]);
    expect([header(1, "x-supermetry-proxy-key"), header(1, "x-supermetry-client-ip")]).toEqual(["smpk_test", "203.0.113.9"]);
    // The script needs no key, and the key never goes out without a visitor address.
    expect(header(2, "x-supermetry-proxy-key")).toBeNull();
    const noIp = createProxyHandler({ fetch, proxyKey: "smpk_test", getClientIp: () => undefined });
    await noIp(new Request("https://shop.example.com/api/public/v1/events", { method: "POST", body: "{}" }));
    expect([header(3, "x-supermetry-proxy-key"), header(3, "x-supermetry-client-ip")]).toEqual([null, null]);
  });

  test("rejects other paths and methods", async () => {
    const { sent, fetch } = upstream(() => new Response(null));
    const handler = createProxyHandler({ host: "https://analytics.example.com", fetch });

    expect((await handler(new Request("https://shop.example.com/api/public/v1/events"))).status).toBe(404);
    expect((await handler(new Request("https://shop.example.com/js/../admin", { method: "POST" }))).status).toBe(404);
    expect((await handler(new Request("https://shop.example.com/secret.txt"))).status).toBe(404);
    expect((await handler(new Request("https://shop.example.com/api/replay/start"))).status).toBe(404);
    expect((await handler(new Request("https://shop.example.com/api/replay/css/../../admin", { method: "PUT", body: "x" }))).status).toBe(404);
    expect((await handler(new Request("https://shop.example.com/replay.js", { method: "POST" }))).status).toBe(404);
    expect(sent).toHaveLength(0);
  });

  test("answers 502 when the upstream is unreachable", async () => {
    const handler = createProxyHandler({
      host: "https://analytics.example.com",
      fetch: (async () => {
        throw new Error("offline");
      }) as unknown as typeof fetch,
    });

    const response = await handler(new Request("https://shop.example.com/api/public/v1/events", { method: "POST", body: "{}" }));
    expect(response.status).toBe(502);
  });
});
