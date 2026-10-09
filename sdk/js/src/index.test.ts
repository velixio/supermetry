import { afterEach, describe, expect, test } from "bun:test";

import { init, type TrackerEvent } from "./index";

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
      tracker.consent(true);
    }).not.toThrow();
  });

  test("validates required browser options", () => {
    installBrowser();

    expect(() => init({ src: "/script.js" } as never)).toThrow(TypeError);
    expect(() => init({ site: "site-id" } as never)).toThrow(TypeError);
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
    tracker.track("Signup", { plan: "pro" });
    tracker.consent(true);

    const calls: unknown[][] = [];
    Object.assign((window as unknown as { supermetry: Record<string, unknown> }).supermetry, {
      pageview: (...args: unknown[]) => calls.push(["pageview", ...args]),
      track: (...args: unknown[]) => calls.push(["track", ...args]),
      consent: (...args: unknown[]) => calls.push(["consent", ...args]),
    });
    script.dispatch("load");

    expect(calls).toEqual([
      ["pageview", { url: "https://example.com/pricing", props: { plan: "pro" } }],
      ["track", "Signup", { plan: "pro" }],
      ["consent", true],
    ]);

    tracker.track("Purchase");
    expect(calls.at(-1)).toEqual(["track", "Purchase", undefined]);
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

    expect(calls).toEqual([["Queued", undefined]]);
  });

  test("omits attributes for omitted options", () => {
    const { script } = installBrowser();

    init({ site: "site-id", src: "/script.js" });

    expect(Object.fromEntries(script.attributes)).toEqual({ "data-site": "site-id" });
  });
});
