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
  test("requires host", () => {
    expect(() => createProxyHandler({} as never)).toThrow(TypeError);
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

  test("rejects other paths and methods", async () => {
    const { sent, fetch } = upstream(() => new Response(null));
    const handler = createProxyHandler({ host: "https://analytics.example.com", fetch });

    expect((await handler(new Request("https://shop.example.com/api/public/v1/events"))).status).toBe(404);
    expect((await handler(new Request("https://shop.example.com/js/../admin", { method: "POST" }))).status).toBe(404);
    expect((await handler(new Request("https://shop.example.com/secret.txt"))).status).toBe(404);
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
