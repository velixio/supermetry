import { afterEach, describe, expect, test } from "bun:test";

import { getTracker, init, type TrackerEvent } from "./index";

type Listener = () => void;

class FakeScript {
  async = false;
  defer = false;
  src = "";
  readonly attributes = new Map<string, string>();
  private readonly listeners = new Map<string, Listener[]>();

  setAttribute(name: string, value: string): void {
    this.attributes.set(name, value);
  }

  addEventListener(name: string, listener: Listener): void {
    const listeners = this.listeners.get(name) ?? [];
    listeners.push(listener);
    this.listeners.set(name, listeners);
  }

  dispatch(name: string): void {
    for (const listener of this.listeners.get(name) ?? []) listener();
  }
}

function installBrowser(): { script: FakeScript; appended: FakeScript[] } {
  const script = new FakeScript();
  const appended: FakeScript[] = [];
  Object.assign(globalThis, {
    document: {
      createElement: (tag: string) => {
        expect(tag).toBe("script");
        return script;
      },
      head: { append: (element: FakeScript) => appended.push(element) },
    },
    window: {},
  });
  return { script, appended };
}

afterEach(() => {
  Reflect.deleteProperty(globalThis, "document");
  Reflect.deleteProperty(globalThis, "window");
});

describe("init", () => {
  test("returns no-op methods during server rendering", () => {
    const tracker = init({ site: "site-id", src: "https://supermetry.test/script.js" });

    expect(() => {
      tracker.pageview();
      tracker.track("Signup", { plan: "pro" });
      tracker.track("Purchase", { items: 2, gift: false }, { revenue: { amount: "49.90", currency: "EUR" } });
      tracker.identify("user-1", { plan: "pro" });
      tracker.consent(true);
      tracker.optIn();
      tracker.optOut();
    }).not.toThrow();
  });

  test("validates required browser options", () => {
    installBrowser();

    expect(() => init({ src: "/script.js" } as never)).toThrow(TypeError);
  });

  test("defaults to Supermetry Cloud when host and src are omitted", () => {
    const { script } = installBrowser();

    init({ site: "site-id" });

    expect(script.src).toBe("https://app.supermetry.com/js/script.js");
    expect(script.attributes.get("data-api")).toBe("https://app.supermetry.com/api/public/v1/events");
  });

  test("configures one script and forwards queued calls after load", () => {
    const { script, appended } = installBrowser();
    const beforeSend = (event: TrackerEvent): TrackerEvent => ({
      ...event,
      props: { source: "sdk" },
    });

    const tracker = init({
      site: "site-id",
      src: "https://supermetry.test/script.js",
      api: "https://supermetry.test/events",
      manual: true,
      hash: true,
      consent: true,
      beforeSend,
      forms: false,
      outbound: false,
      downloads: false,
      allowLocalhost: true,
    });
    const again = init({ site: "ignored", src: "/ignored.js" });

    expect(again).toBe(tracker);
    expect(appended).toEqual([script]);
    expect(script.async).toBe(true);
    expect(script.defer).toBe(true);
    expect(script.src).toBe("https://supermetry.test/script.js");
    expect(Object.fromEntries(script.attributes)).toEqual({
      "data-site": "site-id",
      "data-api": "https://supermetry.test/events",
      "data-manual": "true",
      "data-hash": "true",
      "data-consent": "required",
      "data-forms": "false",
      "data-outbound": "false",
      "data-downloads": "false",
      "data-allow-localhost": "",
    });
    expect((window as unknown as { supermetry: { beforeSend: unknown } }).supermetry.beforeSend).toBe(beforeSend);

    tracker.pageview({ url: "https://example.com/pricing", props: { plan: "pro" } });
    tracker.track("Signup", { plan: "pro", seats: 3, trial: true });
    tracker.track("Purchase", undefined, { revenue: { amount: 49.9, currency: "USD" } });
    tracker.identify("user-1", { plan: "pro" });
    tracker.identify(null);
    tracker.consent(true);
    tracker.optOut();
    tracker.optIn();

    const calls: unknown[][] = [];
    const record = (method: string) => (...args: unknown[]) => calls.push([method, ...args]);
    Object.assign((window as unknown as { supermetry: Record<string, unknown> }).supermetry, {
      pageview: record("pageview"),
      track: record("track"),
      identify: record("identify"),
      consent: record("consent"),
      optIn: record("optIn"),
      optOut: record("optOut"),
    });
    script.dispatch("load");

    expect(calls).toEqual([
      ["pageview", { url: "https://example.com/pricing", props: { plan: "pro" } }],
      ["track", "Signup", { plan: "pro", seats: 3, trial: true }, undefined],
      ["track", "Purchase", undefined, { revenue: { amount: 49.9, currency: "USD" } }],
      ["identify", "user-1", { plan: "pro" }],
      ["identify", null],
      ["consent", true],
      ["optOut"],
      ["optIn"],
    ]);

    tracker.track("Purchase");
    expect(calls.at(-1)).toEqual(["track", "Purchase", undefined, undefined]);
  });

  test("queues calls made before the tracker creates window.supermetry", () => {
    const { script } = installBrowser();
    const tracker = init({ site: "site-id", src: "/script.js" });

    expect((window as unknown as { supermetry?: unknown }).supermetry).toBeUndefined();
    tracker.track("Queued");

    const calls: unknown[][] = [];
    (window as unknown as { supermetry: unknown }).supermetry = {
      track: (...args: unknown[]) => calls.push(args),
    };
    script.dispatch("load");

    expect(calls).toEqual([["Queued", undefined, undefined]]);
  });

  test("queues calls made before init and returns the shared tracker", () => {
    const { script } = installBrowser();
    getTracker().identify("user-1");

    const tracker = init({ site: "site-id", src: "/script.js" });
    expect(tracker).toBe(getTracker());

    const calls: unknown[][] = [];
    (window as unknown as { supermetry: unknown }).supermetry = {
      track: () => {},
      identify: (...args: unknown[]) => calls.push(args),
    };
    script.dispatch("load");

    expect(calls).toEqual([["user-1"]]);
  });

  test("waits for the tracker API when the loaded script swaps in the full build", async () => {
    const { script } = installBrowser();
    const tracker = init({ site: "site-id", src: "/js/script.js" });
    tracker.track("Late");
    script.dispatch("load");

    const calls: unknown[][] = [];
    (window as unknown as { supermetry: unknown }).supermetry = {
      track: (...args: unknown[]) => calls.push(args),
    };
    await Bun.sleep(80);

    expect(calls).toEqual([["Late", undefined, undefined]]);
  });

  test("derives script and endpoint from host", () => {
    const { script } = installBrowser();

    init({ site: "site-id", host: "https://analytics.example.com/", manual: true });

    expect(script.src).toBe("https://analytics.example.com/js/script.full.js");
    expect(script.attributes.get("data-api")).toBe("https://analytics.example.com/api/public/v1/events");
  });

  test("uses first-party paths for an empty host", () => {
    const { script } = installBrowser();

    init({ site: "site-id", host: "", profiles: false });

    expect(script.src).toBe("/js/script.js");
    expect(Object.fromEntries(script.attributes)).toEqual({
      "data-site": "site-id",
      "data-api": "/api/public/v1/events",
      "data-profiles": "false",
    });
  });

  test("omits attributes for omitted options", () => {
    const { script } = installBrowser();

    init({ site: "site-id", src: "/script.js" });

    expect(Object.fromEntries(script.attributes)).toEqual({ "data-site": "site-id" });
  });
});
