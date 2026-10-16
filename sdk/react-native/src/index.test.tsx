import { afterAll, beforeAll, beforeEach, describe, expect, mock, test } from "bun:test";
import { createServer, type IncomingHttpHeaders, type Server } from "node:http";
import type { AddressInfo } from "node:net";

// --- react-native mock -------------------------------------------------------
type Listener = (state: string) => void;
const appStateListeners = new Set<Listener>();
const platform: { OS: string; Version: number | string; isPad?: boolean; constants: Record<string, unknown> } = {
  OS: "ios",
  Version: "17.0",
  constants: {},
};
mock.module("react-native", () => ({
  AppState: {
    currentState: "active",
    addEventListener: (_type: string, listener: Listener) => {
      appStateListeners.add(listener);
      return { remove: () => appStateListeners.delete(listener) };
    },
  },
  Platform: platform,
  Dimensions: { get: () => ({ width: 390.4, height: 844, scale: 3, fontScale: 1 }) },
}));
const emitAppState = (state: string) => appStateListeners.forEach((l) => l(state));

const sdk = await import("./index");
const { createSupermetry, buildUserAgent, screenPath, SupermetryProvider, useSupermetry, trackNavigation } = sdk;
const React = await import("react");
const { renderToString } = await import("react-dom/server");

// --- helpers -----------------------------------------------------------------
interface Call {
  url: string;
  headers: Record<string, string>;
  body: Record<string, any>;
}

function fakeFetch(statuses: Array<number | Error> = [202]) {
  const calls: Call[] = [];
  const fn = (async (url: string, init: RequestInit) => {
    calls.push({ url, headers: init.headers as Record<string, string>, body: JSON.parse(String(init.body)) });
    const next = statuses.length > 1 ? statuses.shift()! : statuses[0]!;
    if (next instanceof Error) throw next;
    return new Response(null, { status: next });
  }) as unknown as typeof fetch;
  return { fn, calls };
}

function memoryStorage(initial: Record<string, string> = {}) {
  const data = new Map(Object.entries(initial));
  return {
    data,
    getItem: async (k: string) => data.get(k) ?? null,
    setItem: async (k: string, v: string) => void data.set(k, v),
    removeItem: async (k: string) => void data.delete(k),
  };
}

const tick = (ms = 5) => new Promise((r) => setTimeout(r, ms));

const base = { site: "app_site", host: "https://a.example.com", appUrl: "https://app.example.com/", retryDelay: 1 };

beforeEach(() => {
  appStateListeners.clear();
  platform.OS = "ios";
  platform.Version = "17.0";
  platform.isPad = false;
  platform.constants = {};
});

// --- tests -------------------------------------------------------------------
describe("platform", () => {
  test("browser-shaped user agents", () => {
    expect(buildUserAgent()).toBe("Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Supermetry-ReactNative/0.2.0");
    platform.isPad = true;
    expect(buildUserAgent()).toBe("Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) Supermetry-ReactNative/0.2.0");
    Object.assign(platform, { OS: "android", Version: 34, constants: { Release: "14", Model: "Pixel 8" } });
    expect(buildUserAgent()).toBe("Mozilla/5.0 (Linux; Android 14; Pixel 8) Supermetry-ReactNative/0.2.0");
    Object.assign(platform, { constants: { Model: "Weird; (Model)" } });
    expect(buildUserAgent()).toBe("Mozilla/5.0 (Linux; Android 34; Weird Model) Supermetry-ReactNative/0.2.0");
  });

  test("screen paths are slugified", () => {
    expect(screenPath("Checkout")).toBe("checkout");
    expect(screenPath("Product Details")).toBe("product-details");
    expect(screenPath("ProductDetails")).toBe("product-details");
    expect(screenPath("/Settings/Edit Profile/")).toBe("settings/edit-profile");
    expect(screenPath("Café ★")).toBe("cafe");
  });
});

describe("client", () => {
  test("host defaults to Supermetry Cloud", async () => {
    const { fn, calls } = fakeFetch();
    const { host: _host, ...cloud } = base;
    const sm = createSupermetry({ ...cloud, fetch: fn });
    sm.screen("Home");
    await sm.flush();
    expect(calls[0]!.url).toBe("https://app.supermetry.com/api/public/v1/events");
    sm.destroy();
  });

  test("screen sends a pageview to appUrl + slug with UA and screen width", async () => {
    const { fn, calls } = fakeFetch();
    const sm = createSupermetry({ ...base, fetch: fn });
    sm.screen("Product Details", { sku: "A1" });
    await sm.flush();
    expect(calls).toHaveLength(1);
    const { url, headers, body } = calls[0]!;
    expect(url).toBe("https://a.example.com/api/public/v1/events");
    expect(headers["Content-Type"]).toBe("application/json");
    expect(headers["User-Agent"]).toBe("Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Supermetry-ReactNative/0.2.0");
    expect(body).toMatchObject({
      v: 1,
      site: "app_site",
      name: "pageview",
      url: "https://app.example.com/product-details",
      screen_width: 390,
      props: { sku: "A1" },
    });
    expect(body.id).toMatch(/^[0-9a-f]{32}$/);
    expect(typeof body.ts).toBe("number");
    sm.destroy();
  });

  test("track uses the current screen URL; revenue kept; traits never attached", async () => {
    const { fn, calls } = fakeFetch();
    const sm = createSupermetry({ ...base, fetch: fn });
    sm.track("Open");
    sm.screen("Cart");
    sm.track("Purchase", { items: 2 }, { revenue: { amount: "9.99", currency: "USD" } });
    await sm.flush();
    expect(calls.map((c) => c.body.url)).toEqual([
      "https://app.example.com/",
      "https://app.example.com/cart",
      "https://app.example.com/cart",
    ]);
    expect(calls[2]!.body).toMatchObject({ name: "Purchase", revenue: { amount: "9.99", currency: "USD" } });
    expect(calls.every((c) => c.body.traits === undefined)).toBe(true);
    sm.destroy();
  });

  test("identify persists profile_id and attaches it; reset clears", async () => {
    const { fn, calls } = fakeFetch();
    const storage = memoryStorage();
    const sm = createSupermetry({ ...base, fetch: fn, storage });
    await sm.ready;
    sm.identify("user_1", { plan: "pro", nested: {} as never });
    sm.track("After");
    await sm.flush();
    expect(calls[0]!.body).toMatchObject({ name: "identify", profile_id: "user_1", traits: { plan: "pro" } });
    expect(calls[1]!.body).toMatchObject({ name: "After", profile_id: "user_1" });
    expect(calls[1]!.body.traits).toBeUndefined();
    expect(storage.data.get("supermetry:profile_id")).toBe("user_1");

    sm.identify(null);
    await tick();
    expect(sm.profileId).toBeUndefined();
    expect(storage.data.has("supermetry:profile_id")).toBe(false);
    sm.destroy();
  });

  test("persisted profile_id is attached after relaunch, even to events sent before load", async () => {
    const { fn, calls } = fakeFetch();
    const sm = createSupermetry({ ...base, fetch: fn, storage: memoryStorage({ "supermetry:profile_id": "user_9" }) });
    sm.screen("Home"); // before storage resolves
    await sm.flush();
    expect(sm.profileId).toBe("user_9");
    expect(calls[0]!.body.profile_id).toBe("user_9");
    sm.destroy();
  });

  test("retries network errors and 5xx with the same id and ts; drops on 400", async () => {
    const { fn, calls } = fakeFetch([new TypeError("Network request failed"), 503, 202]);
    const sm = createSupermetry({ ...base, fetch: fn });
    sm.screen("A");
    await sm.flush();
    await tick(30);
    expect(calls).toHaveLength(3);
    expect(new Set(calls.map((c) => c.body.id)).size).toBe(1);
    expect(new Set(calls.map((c) => c.body.ts)).size).toBe(1);

    const rejected = fakeFetch([400, 202]);
    const errors: Error[] = [];
    const sm2 = createSupermetry({ ...base, fetch: rejected.fn, onError: (e) => errors.push(e) });
    sm2.screen("A");
    sm2.screen("B");
    await sm2.flush();
    expect(rejected.calls.map((c) => c.body.url)).toEqual(["https://app.example.com/a", "https://app.example.com/b"]);
    expect(errors).toHaveLength(1);
    sm.destroy();
    sm2.destroy();
  });

  test("drops an event after maxAttempts", async () => {
    const { fn, calls } = fakeFetch([500]);
    const errors: Error[] = [];
    const sm = createSupermetry({ ...base, fetch: fn, maxAttempts: 3, onError: (e) => errors.push(e) });
    sm.screen("A");
    await tick(50);
    expect(calls).toHaveLength(3);
    expect(errors).toHaveLength(1);
    sm.destroy();
  });

  test("flushes on background and persists undelivered events for the next launch", async () => {
    const storage = memoryStorage();
    const offline = fakeFetch([new TypeError("offline")]);
    const sm = createSupermetry({ ...base, fetch: offline.fn, storage, retryDelay: 60_000 });
    sm.screen("Offline");
    await sm.flush();
    const attemptsBefore = offline.calls.length;
    emitAppState("background");
    await tick(20);
    expect(offline.calls.length).toBe(attemptsBefore + 1); // background forced an immediate attempt
    const persisted = JSON.parse(storage.data.get("supermetry:queue")!);
    expect(persisted).toHaveLength(1);
    sm.destroy();

    const online = fakeFetch();
    const relaunched = createSupermetry({ ...base, fetch: online.fn, storage });
    await relaunched.flush();
    expect(online.calls).toHaveLength(1);
    expect(online.calls[0]!.body.id).toBe(persisted[0].id);
    expect(online.calls[0]!.body.ts).toBe(persisted[0].ts);
    expect(storage.data.has("supermetry:queue")).toBe(false);
    relaunched.destroy();
  });

  test("opt-out is persisted and stops tracking", async () => {
    const { fn, calls } = fakeFetch();
    const storage = memoryStorage();
    const sm = createSupermetry({ ...base, fetch: fn, storage });
    await sm.optOut();
    sm.screen("A");
    await sm.flush();
    expect(calls).toHaveLength(0);
    expect(storage.data.get("supermetry:opt_out")).toBe("1");
    sm.destroy();

    const relaunched = createSupermetry({ ...base, fetch: fn, storage });
    relaunched.screen("Before load");
    await relaunched.ready;
    expect(relaunched.isOptedOut()).toBe(true);
    await relaunched.flush();
    expect(calls).toHaveLength(0);
    await relaunched.optIn();
    relaunched.screen("B");
    await relaunched.flush();
    expect(calls).toHaveLength(1);
    relaunched.destroy();
  });

  test("disabled sends nothing; invalid input reports instead of throwing", async () => {
    const { fn, calls } = fakeFetch();
    createSupermetry({ ...base, fetch: fn, disabled: true }).screen("A");
    const errors: Error[] = [];
    const sm = createSupermetry({ ...base, fetch: fn, onError: (e) => errors.push(e) });
    sm.track("");
    sm.track("identify");
    sm.identify("x".repeat(129));
    await sm.flush();
    expect(calls).toHaveLength(0);
    expect(errors).toHaveLength(3);
    expect(() => createSupermetry({ ...base, appUrl: "app.example.com" })).toThrow();
    sm.destroy();
  });
});

describe("react", () => {
  test("useSupermetry returns the provider client", () => {
    const sm = createSupermetry({ ...base, fetch: fakeFetch().fn });
    let seen: unknown;
    function Probe() {
      seen = useSupermetry();
      return null;
    }
    renderToString(React.createElement(SupermetryProvider, { client: sm }, React.createElement(Probe)));
    expect(seen).toBe(sm);
    expect(() => renderToString(React.createElement(Probe))).toThrow(/SupermetryProvider/);
    sm.destroy();
  });

  test("trackNavigation sends a screen per focused route change", async () => {
    const { fn, calls } = fakeFetch();
    const sm = createSupermetry({ ...base, fetch: fn });
    let route: { key: string; name: string } | undefined = { key: "home-1", name: "Home" };
    const ref = { getCurrentRoute: () => route };
    const handlers = trackNavigation(sm, ref);
    handlers.onReady();
    handlers.onStateChange(); // unchanged
    route = { key: "product-1", name: "ProductDetails" };
    trackNavigation(sm, ref).onStateChange(); // fresh handlers share last-route state
    await sm.flush();
    expect(calls.map((c) => c.body.url)).toEqual(["https://app.example.com/home", "https://app.example.com/product-details"]);
    sm.destroy();
  });
});

describe("live HTTP", () => {
  let server: Server;
  let host: string;
  const received: Array<{ method?: string; path?: string; headers: IncomingHttpHeaders; body: string }> = [];

  beforeAll(async () => {
    server = createServer((req, res) => {
      let body = "";
      req.on("data", (c) => (body += c));
      req.on("end", () => {
        received.push({ method: req.method, path: req.url, headers: req.headers, body });
        res.writeHead(received.length === 1 ? 429 : 202, { "Retry-After": "0" }).end();
      });
    });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    host = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  });

  afterAll(() => {
    server.close();
  });

  test("real fetch sends the mobile UA and contract body; 429 is retried with the same id", async () => {
    Object.assign(platform, { OS: "android", Version: 34, constants: { Release: "14", Model: "Pixel 8" } });
    const sm = createSupermetry({ site: "live", host, appUrl: "https://app.example.com", retryDelay: 1 });
    sm.identify("user_live", { tier: "gold" });
    await sm.flush();
    await tick(50);
    expect(received).toHaveLength(2);
    for (const r of received) {
      expect(r.method).toBe("POST");
      expect(r.path).toBe("/api/public/v1/events");
      expect(r.headers["content-type"]).toBe("application/json");
      expect(r.headers["user-agent"]).toBe("Mozilla/5.0 (Linux; Android 14; Pixel 8) Supermetry-ReactNative/0.2.0");
    }
    const [first, second] = received.map((r) => JSON.parse(r.body));
    expect(second).toEqual(first);
    expect(Object.keys(first).sort()).toEqual(["id", "name", "profile_id", "screen_width", "site", "traits", "ts", "url", "v"]);
    expect(first).toMatchObject({
      v: 1,
      site: "live",
      name: "identify",
      url: "https://app.example.com/",
      profile_id: "user_live",
      traits: { tier: "gold" },
      screen_width: 390,
    });
    sm.destroy();
  });
});
