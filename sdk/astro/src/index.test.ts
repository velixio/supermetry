import { describe, expect, test } from "bun:test";

import { pageLoadScript, scriptAttributes } from "./index";

describe("scriptAttributes", () => {
  test("requires site", () => {
    expect(() => scriptAttributes({} as never)).toThrow(TypeError);
  });

  test("defaults to first-party paths", () => {
    expect(scriptAttributes({ site: "site-id" })).toEqual({
      async: true,
      defer: true,
      src: "/js/script.js",
      "data-site": "site-id",
      "data-api": "/api/public/v1/events",
    });
  });

  test("maps options to data attributes", () => {
    expect(
      scriptAttributes({
        site: "site-id",
        host: "https://analytics.example.com/",
        hash: true,
        consent: true,
        outbound: false,
        profiles: false,
        beforeSend: "scrubEvent",
        allowLocalhost: true,
      }),
    ).toEqual({
      async: true,
      defer: true,
      src: "https://analytics.example.com/js/script.full.js",
      "data-site": "site-id",
      "data-api": "https://analytics.example.com/api/public/v1/events",
      "data-hash": "true",
      "data-outbound": "false",
      "data-profiles": "false",
      "data-consent": "required",
      "data-before-send": "scrubEvent",
      "data-allow-localhost": "",
    });
  });

  test("forces manual mode with View Transitions", () => {
    const attributes = scriptAttributes({ site: "site-id", src: "/stats.js", api: "/collect", viewTransitions: true });
    expect(attributes.src).toBe("/stats.js");
    expect(attributes["data-api"]).toBe("/collect");
    expect(attributes["data-manual"]).toBe("true");
  });
});

describe("pageLoadScript", () => {
  test("sends a pageview on every astro:page-load, waiting for the tracker", async () => {
    const listeners: (() => void)[] = [];
    const fakeWindow: Record<string, unknown> = {};
    const location = { href: "https://site.example.com/" };
    const run = new Function("window", "document", "location", pageLoadScript);
    const fakeDocument = { addEventListener: (name: string, listener: () => void) => name === "astro:page-load" && listeners.push(listener) };

    run(fakeWindow, fakeDocument, location);
    run(fakeWindow, fakeDocument, location);
    expect(listeners).toHaveLength(1);

    const pageviews: unknown[] = [];
    listeners[0]!();
    location.href = "https://site.example.com/blog";
    fakeWindow.supermetry = { pageview: (options: unknown) => pageviews.push(options) };
    await Bun.sleep(70);
    listeners[0]!();

    expect(pageviews).toEqual([{ url: "https://site.example.com/" }, { url: "https://site.example.com/blog" }]);
  });
});
