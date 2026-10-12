import { afterEach, describe, expect, test } from "bun:test";

import { createSupermetryProxy } from "./proxy";

const realFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = realFetch;
});

describe("createSupermetryProxy", () => {
  test("exposes GET, HEAD and POST route handlers", async () => {
    const sent: [string, RequestInit][] = [];
    globalThis.fetch = (async (url: string, init: RequestInit) => {
      sent.push([url, init]);
      return new Response(null, { status: 202 });
    }) as unknown as typeof fetch;
    const { GET, HEAD, POST } = createSupermetryProxy({ host: "https://analytics.example.com" });

    const event = await POST(
      new Request("https://shop.example.com/api/public/v1/events", {
        method: "POST",
        body: "{}",
        headers: { "user-agent": "Mozilla/5.0", "x-forwarded-for": "203.0.113.9" },
      }),
    );
    await GET(new Request("https://shop.example.com/js/script.js"));

    expect(HEAD).toBe(GET);
    expect(event.status).toBe(202);
    expect(event.headers.get("cache-control")).toBe("no-store");
    expect(sent.map(([url]) => url)).toEqual([
      "https://analytics.example.com/api/public/v1/events",
      "https://analytics.example.com/js/script.js",
    ]);
    expect((sent[0]![1].headers as Headers).get("x-forwarded-for")).toBe("203.0.113.9");
  });
});
