import { afterEach, describe, expect, test } from "bun:test";

import { createSupermetryProxy } from "./proxy";

const realFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = realFetch;
});

describe("createSupermetryProxy", () => {
  test("serves scripts from the loader and events from the action", async () => {
    const sent: [string, RequestInit][] = [];
    globalThis.fetch = (async (url: string, init: RequestInit) => {
      sent.push([url, init]);
      return new Response(url.endsWith(".js") ? "js" : null, { status: url.endsWith(".js") ? 200 : 202 });
    }) as unknown as typeof fetch;
    const { loader, action } = createSupermetryProxy({ host: "https://analytics.example.com" });

    const script = await loader({ request: new Request("https://shop.example.com/js/script.full.js") });
    const event = await action({
      request: new Request("https://shop.example.com/api/public/v1/events", {
        method: "POST",
        body: "{}",
        headers: { "user-agent": "Mozilla/5.0", "x-forwarded-for": "203.0.113.9, 10.0.0.2" },
      }),
    });

    expect(await script.text()).toBe("js");
    expect(event.status).toBe(202);
    expect(event.headers.get("cache-control")).toBe("no-store");
    expect(sent.map(([url]) => url)).toEqual([
      "https://analytics.example.com/js/script.full.js",
      "https://analytics.example.com/api/public/v1/events",
    ]);
    const headers = sent[1]![1].headers as Headers;
    expect(headers.get("user-agent")).toBe("Mozilla/5.0");
    expect(headers.get("x-forwarded-for")).toBe("203.0.113.9");
  });
});
