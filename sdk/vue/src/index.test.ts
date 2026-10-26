import { afterEach, describe, expect, test } from "bun:test";
import { createApp } from "vue";
import { createMemoryHistory, createRouter } from "vue-router";

import Supermetry, { getTracker, useSupermetry } from "./index";

type Listener = () => void;

class FakeScript {
  async = false;
  defer = false;
  src = "";
  readonly attributes = new Map<string, string>();
  private readonly listeners: Listener[] = [];

  setAttribute(name: string, value: string): void {
    this.attributes.set(name, value);
  }

  addEventListener(_name: string, listener: Listener): void {
    this.listeners.push(listener);
  }

  dispatch(): void {
    for (const listener of this.listeners) listener();
  }
}

function installBrowser(href: () => string): FakeScript {
  const script = new FakeScript();
  Object.assign(globalThis, {
    document: { createElement: () => script, head: { append() {} } },
    window: {
      get location() {
        return { href: href() };
      },
    },
  });
  return script;
}

afterEach(() => {
  Reflect.deleteProperty(globalThis, "document");
  Reflect.deleteProperty(globalThis, "window");
});

describe("Supermetry plugin", () => {
  test("is a no-op during server rendering", () => {
    const app = createApp({ render: () => null });
    expect(() => app.use(Supermetry, { site: "site-id", host: "https://analytics.supermetry.com" })).not.toThrow();
    expect(app.config.globalProperties.$supermetry).toBe(getTracker());
    expect(() => getTracker().track("Ignored")).not.toThrow();
  });

  test("provides the tracker to useSupermetry", () => {
    installBrowser(() => "https://shop.supermetry.com/");
    let injected: unknown;
    const app = createApp({ render: () => null });
    app.use(Supermetry, { site: "site-id", host: "https://analytics.supermetry.com", outbound: true });
    app.runWithContext(() => {
      injected = useSupermetry();
    });

    expect(injected).toBe(getTracker());
    expect(useSupermetry()).toBe(getTracker());
  });

  test("tracks router navigations in manual mode", async () => {
    let path = "/";
    const script = installBrowser(() => `https://shop.supermetry.com${path}`);
    const view = { render: () => null };
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: "/", component: view },
        { path: "/pricing", component: view },
        { path: "/blocked", component: view, beforeEnter: () => false },
      ],
    });
    router.afterEach((to) => {
      path = to.fullPath;
    });

    const app = createApp({ render: () => null });
    app.use(Supermetry, { site: "site-id", host: "https://analytics.supermetry.com", router });

    expect(script.src).toBe("https://analytics.supermetry.com/js/script.full.js");
    expect(script.attributes.get("data-manual")).toBe("true");

    const pageviews: unknown[] = [];
    (window as unknown as { supermetry: unknown }).supermetry = {
      track() {},
      pageview: (options: unknown) => pageviews.push(options),
    };
    script.dispatch();

    await router.push("/");
    await router.push("/pricing");
    await router.push("/blocked");

    expect(pageviews).toEqual([{ url: "https://shop.supermetry.com/" }, { url: "https://shop.supermetry.com/pricing" }]);
  });
});
