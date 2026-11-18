import { describe, expect, test } from "bun:test";
import { createClient, type Options } from "../src/index";
import { FakeServer } from "./fake-server";

function setup(over: Partial<Options> = {}) {
  const server = new FakeServer();
  const sleeps: number[] = [];
  const drops: { reason: string; n: number }[] = [];
  const client = createClient({
    appId: "app_1",
    ingestKey: "k_test",
    host: "https://example.test",
    release: "1.2.3",
    transport: server.transport,
    flushIntervalMs: 0,
    sleep: async (ms) => void sleeps.push(ms),
    random: () => 0.5,
    onDrop: (events, reason) => void drops.push({ reason, n: events.length }),
    ...over,
  });
  return { server, client, sleeps, drops };
}

describe("request shape", () => {
  test("sends key header and {site, events} body to the intake path", async () => {
    const { server, client } = setup();
    client.captureEvent("hang", { tags: { a: 1 } });
    await client.flush();
    const r = server.requests[0]!;
    expect(r.headers["X-Supermetry-Key"]).toBe("k_test");
    expect(r.headers["Content-Type"]).toBe("application/json");
    expect(r.parsed.site).toBe("app_1");
    expect(Object.keys(r.parsed).sort()).toEqual(["events", "site"]);
    const e = r.parsed.events[0]!;
    expect(e.id).toMatch(/^[0-9a-f]{32}$/);
    expect(e.kind).toBe("hang");
    expect(e.release).toBe("1.2.3");
    expect(typeof e.ts).toBe("number");
    expect(["windows", "macos", "linux", "ios", "android", "web", "other"]).toContain(e.platform);
    expect(e.runtime).toBeTruthy();
    expect(e.install_id).toBeUndefined();
  });

  test("installId is only sent when supplied", async () => {
    const { server, client } = setup({ installId: "inst-1" });
    client.captureEvent("hang", {});
    await client.flush();
    expect(server.accepted[0]!.install_id).toBe("inst-1");
  });

  test("captureCrash builds a fatal crash event", async () => {
    const { server, client } = setup();
    client.captureCrash({ errorType: "SIGSEGV", exitReason: "signal", signal: "SIGSEGV", exitCode: 139, tags: { gpu: "cuda" } });
    await client.flush();
    const e = server.accepted[0]!;
    expect(e).toMatchObject({ kind: "crash", level: "fatal", handled: false, error_type: "SIGSEGV", exit_code: 139, signal: "SIGSEGV" });
  });

  test("startSession sends one session event per process and tags later events", async () => {
    const { server, client } = setup();
    client.startSession();
    client.startSession();
    client.captureEvent("hang", {});
    await client.flush();
    const sessions = server.accepted.filter((e) => e.kind === "session");
    expect(sessions.length).toBe(1);
    expect(sessions[0]!.session_id).toMatch(/^[0-9a-f]{32}$/);
    expect(server.accepted.find((e) => e.kind === "hang")!.session_id).toBe(sessions[0]!.session_id);
  });
});

describe("captureException", () => {
  test("never sends the message, but sends class name and scrubbed frames", async () => {
    const { server, client } = setup();
    class DiskFullError extends Error {}
    const secret = "user bob@example.org wrote /Users/bob/secret-notes.txt";
    client.captureException(new DiskFullError(secret), { handled: true, tags: { op: "save" } });
    await client.flush();
    const body = server.requests[0]!.body;
    expect(body).not.toContain("secret-notes");
    expect(body).not.toContain("bob@example.org");
    expect(body).not.toContain(secret);
    const e = server.accepted[0]!;
    expect(e.kind).toBe("error");
    expect(e.error_type).toBe("DiskFullError");
    expect(e.handled).toBe(true);
    expect(e.frames).toContain("client.test.ts");
    expect(e.frames).not.toContain("/home/");
  });

  test("non-Error values do not leak", async () => {
    const { server, client } = setup();
    client.captureException("password=hunter2");
    await client.flush();
    expect(server.requests[0]!.body).not.toContain("hunter2");
    expect(server.accepted[0]!.error_type).toBe("Error");
  });
});

describe("queue", () => {
  test("splits batches at 20 events", async () => {
    const { server, client } = setup();
    for (let i = 0; i < 45; i++) client.captureEvent("hang", {});
    await client.flush();
    expect(server.requests.map((r) => r.parsed.events.length)).toEqual([20, 20, 5]);
  });

  test("splits batches at 16 KB", async () => {
    const big = setup();
    // 12 events of ~2 KB of frames each: 24 KB, so more than one request is needed
    for (let i = 0; i < 12; i++) big.client.captureEvent("hang", { frames: Array.from({ length: 100 }, (_, j) => `src/f${i}_${j}.js:1 fn`).join("\n") });
    await big.client.flush();
    expect(big.server.requests.length).toBeGreaterThan(1);
    for (const r of big.server.requests) {
      expect(new TextEncoder().encode(r.body).length).toBeLessThanOrEqual(16384);
      expect(r.parsed.events.length).toBeLessThanOrEqual(20);
    }
    expect(big.server.accepted.length).toBe(12);
  });

  test("bounded queue drops oldest first", async () => {
    const { server, client, drops } = setup({ maxQueue: 3 });
    for (let i = 0; i < 5; i++) client.captureEvent("hang", { tags: { i } });
    await client.flush();
    expect(server.accepted.map((e) => e.tags.i)).toEqual([2, 3, 4]);
    expect(drops).toContainEqual({ reason: "queue_full", n: 1 });
  });

  test("retries 429 and 5xx with backoff and the same ids", async () => {
    const { server, client, sleeps } = setup();
    server.script = [429, 503, "network"];
    client.captureEvent("hang", {});
    await client.flush();
    expect(server.requests.length).toBe(4);
    const ids = new Set(server.requests.map((r) => r.parsed.events[0]!.id));
    expect(ids.size).toBe(1);
    expect(server.accepted.length).toBe(1);
    // 1s, 2s, 4s with jitter factor 0.5 -> 0.75x..1.25x range centred; random=0.5 means no jitter
    expect(sleeps).toEqual([1000, 2000, 4000]);
  });

  test("backoff is capped at 5 minutes", async () => {
    const { server, client, sleeps } = setup();
    server.script = Array(12).fill(500);
    client.captureEvent("hang", {});
    await client.flush(1e9);
    expect(Math.max(...sleeps)).toBe(300_000);
  });

  test("stops on 401 and calls onDrop", async () => {
    const { server, client, drops } = setup();
    server.script = [401];
    client.captureEvent("hang", {});
    await client.flush();
    expect(server.requests.length).toBe(1);
    expect(drops).toEqual([{ reason: "http_401", n: 1 }]);
    await client.flush();
    expect(server.requests.length).toBe(1);
  });

  test("stops on 400", async () => {
    const { server, client, drops } = setup();
    server.script = [400];
    client.captureEvent("hang", {});
    await client.flush();
    expect(server.requests.length).toBe(1);
    expect(drops[0]!.reason).toBe("http_400");
  });

  test("setEnabled(false) clears the queue and stops sending", async () => {
    const { server, client } = setup();
    client.captureEvent("hang", {});
    client.setEnabled(false);
    client.captureEvent("hang", {});
    await client.flush();
    expect(server.requests.length).toBe(0);
    client.setEnabled(true);
    client.captureEvent("hang", {});
    await client.flush();
    expect(server.accepted.length).toBe(1);
  });

  test("enabled: false at init sends nothing", async () => {
    const { server, client } = setup({ enabled: false });
    client.captureEvent("hang", {});
    await client.flush();
    expect(server.requests.length).toBe(0);
  });

  test("discards events older than maxAgeDays", async () => {
    let now = Date.UTC(2026, 10, 1);
    const { server, client, drops } = setup({ now: () => now, maxAgeDays: 7 });
    client.captureEvent("hang", {});
    now += 8 * 86400_000;
    client.captureEvent("hang", {});
    await client.flush();
    expect(server.accepted.length).toBe(1);
    expect(drops).toContainEqual({ reason: "too_old", n: 1 });
  });

  test("flush resolves when the queue is empty, and with nothing queued", async () => {
    const { server, client } = setup();
    await client.flush();
    expect(server.requests.length).toBe(0);
    client.captureEvent("hang", {});
    await client.flush();
    expect(server.accepted.length).toBe(1);
  });

  test("flush gives up at its timeout while the server keeps failing", async () => {
    const { server, client } = setup({ sleep: async () => {} });
    server.script = Array(1000).fill(503);
    client.captureEvent("hang", {});
    const t = Date.now();
    await client.flush(50);
    expect(Date.now() - t).toBeLessThan(2000);
  });

  test("reports server-side drops through onDrop", async () => {
    const { server, client, drops } = setup();
    server.forceDrop = "invalid";
    client.captureEvent("hang", {});
    await client.flush();
    expect(drops).toEqual([{ reason: "invalid", n: 1 }]);
    expect(server.requests.length).toBe(1);
  });

  test("storage adapter restores unsent events and saves the queue", async () => {
    const saved: any[][] = [];
    const first = setup({ storage: { load: () => [], save: (e) => void saved.push(e) } });
    first.server.script = [503];
    first.client.captureEvent("hang", {});
    await first.client.flush(1);
    expect(saved.at(-1)!.length).toBe(1);
    const second = setup({ storage: { load: () => saved.at(-1)!, save: () => {} } });
    await second.client.flush();
    expect(second.server.accepted[0]!.id).toBe(saved.at(-1)![0].id);
  });

  test("shutdown flushes and then ignores new events", async () => {
    const { server, client } = setup();
    client.captureEvent("hang", {});
    await client.shutdown();
    expect(server.accepted.length).toBe(1);
    client.captureEvent("hang", {});
    await client.flush();
    expect(server.accepted.length).toBe(1);
  });
});

describe("product events", () => {
  test("track sends kind event with name and props, no level or user id", async () => {
    const { server, client } = setup({ installId: "inst-1" });
    client.track("purchase", { plan: "pro", $release: "9", n: 5 as unknown as string });
    await client.flush();
    const e = server.accepted[0]!;
    expect(e.kind).toBe("event");
    expect(e.name).toBe("purchase");
    expect(e.props).toEqual({ plan: "pro" });
    expect(e.install_id).toBe("inst-1");
    expect(e.screen).toBeUndefined();
    expect(e.level).toBeUndefined();
    expect(e.handled).toBeUndefined();
    expect(e.user_id).toBeUndefined();
  });

  test("screen sends kind screen with the screen name", async () => {
    const { server, client } = setup({ installId: "inst-1" });
    client.screen("paywall", { from: "home" });
    await client.flush();
    const e = server.accepted[0]!;
    expect(e.kind).toBe("screen");
    expect(e.screen).toBe("paywall");
    expect(e.props).toEqual({ from: "home" });
    expect(e.name).toBeUndefined();
    expect(e.level).toBeUndefined();
  });

  test("rejects invalid names and calls without an install id", async () => {
    const { server, client } = setup({ installId: "inst-1" });
    client.track("$app_open");
    client.track("");
    client.track("bad/name");
    client.track("x".repeat(65));
    client.screen("");
    await client.flush();
    expect(server.requests.length).toBe(0);
    const anon = setup();
    anon.client.track("purchase");
    anon.client.screen("home");
    await anon.client.flush();
    expect(anon.server.requests.length).toBe(0);
  });

  test("props are capped at 20 keys and 200 bytes", async () => {
    const { server, client } = setup({ installId: "inst-1" });
    const props: Record<string, string> = {};
    for (let i = 0; i < 25; i++) props[`k${i}`] = "v".repeat(300);
    client.track("many", props);
    await client.flush();
    const sent = server.accepted[0]!.props as Record<string, string>;
    expect(Object.keys(sent).length).toBe(20);
    expect(sent.k0!.length).toBe(200);
  });

  test("honours the batch cap of 20 per request", async () => {
    const { server, client } = setup({ installId: "inst-1" });
    for (let i = 0; i < 25; i++) client.track(`e${i}`);
    await client.flush();
    expect(server.requests.map((r) => r.parsed.events.length)).toEqual([20, 5]);
  });

  test("a disabled client records nothing", async () => {
    const { server, client } = setup({ installId: "inst-1", enabled: false });
    client.track("purchase");
    await client.flush();
    expect(server.requests.length).toBe(0);
  });
});
