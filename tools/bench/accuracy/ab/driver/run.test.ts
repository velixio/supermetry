import { afterEach, describe, expect, test } from "bun:test";
import { existsSync } from "node:fs";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";

import { runDriver, type Scenario } from "./run";
import { startSite, type SiteServer } from "../site/server";

const trackerFailure =
  "tracker did not fire: check navigator.webdriver, host name, Do-Not-Track, UA";

function hasChromium(): boolean {
  return existsSync(chromium.executablePath());
}

type Seen = {
  endpoint: "plausible" | "supermetry";
  ip: string;
  userAgent: string;
  name: string;
  revenue?: { amount: number; currency: string };
};

function fakeTracker(endpoint: Seen["endpoint"], origin: string): string {
  const path = endpoint === "plausible" ? "/api/event" : "/api/public/v1/events";
  const install = endpoint === "plausible"
    ? `window.plausible=(name, options={})=>send(name, options);`
    : `window.supermetry={track:(name, props, options={})=>send(name, {props, ...options})};`;
  return `(() => {
    const send = (name, detail = {}) => fetch(${JSON.stringify(origin + path)}, {
      method: "POST",
      keepalive: true,
      headers: {"content-type": "application/json"},
      body: JSON.stringify({name, ...detail})
    });
    ${install}
    send("pageview");
    const pushState = history.pushState.bind(history);
    history.pushState = (...args) => { pushState(...args); send("pageview"); };
    document.addEventListener("click", event => {
      const link = event.target.closest("a");
      if (!link) return;
      if (link.href.endsWith(".pdf")) send("File Download");
      else if (link.origin !== location.origin) send("Outbound Link: Click");
    }, true);
  })();`;
}

function startTracker(scripts = true): { server: ReturnType<typeof Bun.serve>; seen: Seen[]; scriptLoads: Map<string, number> } {
  const seen: Seen[] = [];
  const scriptLoads = new Map<string, number>();
  const server = Bun.serve({
    port: 0,
    async fetch(request) {
      const url = new URL(request.url);
      const ip = request.headers.get("x-forwarded-for") ?? "";
      if (url.pathname.startsWith("/js/")) {
        scriptLoads.set(ip, (scriptLoads.get(ip) ?? 0) + 1);
        if (!scripts) return new Response("missing", { status: 404 });
        const product = url.pathname.includes("script.") ? "plausible" : "supermetry";
        return new Response(fakeTracker(product, url.origin), {
          headers: { "content-type": "application/javascript", "access-control-allow-origin": "*" },
        });
      }
      if (request.method === "POST" && (url.pathname === "/api/event" || url.pathname === "/api/public/v1/events")) {
        const body = await request.json() as { name?: string; revenue?: { amount: number; currency: string } };
        seen.push({
          endpoint: url.pathname === "/api/event" ? "plausible" : "supermetry",
          ip,
          userAgent: request.headers.get("user-agent") ?? "",
          name: body.name ?? "",
          revenue: body.revenue,
        });
        return new Response(null, { status: 202, headers: { "access-control-allow-origin": "*" } });
      }
      if (request.method === "OPTIONS") {
        return new Response(null, {
          status: 204,
          headers: {
            "access-control-allow-origin": "*",
            "access-control-allow-headers": "content-type,x-forwarded-for",
          },
        });
      }
      return new Response("not found", { status: 404 });
    },
  });
  return { server, seen, scriptLoads };
}

const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/140.0.0.0 Safari/537.36";
const baseStep = { ms: 0, path: "", name: "", props: {}, revenue: null, currency: "USD", href: "" };
const scenario: Scenario = {
  seed: 7,
  visits: [
    { id: "bounce", ip: "198.51.100.11", user_agent: userAgent, viewport: [1366, 768], device: "desktop", browser: "Chrome", os: "Linux", referrer: "", utm: {}, start_offset_ms: 0, steps: [{ ...baseStep, kind: "goto", path: "/" }] },
    { id: "journey", ip: "198.51.100.12", user_agent: userAgent, viewport: [1366, 768], device: "desktop", browser: "Chrome", os: "Linux", referrer: "https://www.google.com/", utm: {}, start_offset_ms: 10, steps: [{ ...baseStep, kind: "goto", path: "/pricing" }, { ...baseStep, kind: "goto", path: "/about" }] },
    { id: "spa", ip: "198.51.100.13", user_agent: userAgent, viewport: [390, 844], device: "mobile", browser: "Chrome", os: "Android", referrer: "", utm: {}, start_offset_ms: 20, steps: [{ ...baseStep, kind: "goto", path: "/spa" }, { ...baseStep, kind: "spa", path: "/spa/a" }, { ...baseStep, kind: "spa", path: "/spa/b" }] },
    { id: "actions", ip: "198.51.100.14", user_agent: userAgent, viewport: [1366, 768], device: "desktop", browser: "Chrome", os: "Linux", referrer: "", utm: {}, start_offset_ms: 30, steps: [{ ...baseStep, kind: "goto", path: "/" }, { ...baseStep, kind: "event", name: "Purchase", props: { plan: "pro" }, revenue: 49 }, { ...baseStep, kind: "download", href: "/files/report.pdf" }, { ...baseStep, kind: "outbound", href: "https://supermetry.com/out" }] },
  ],
};

let site: SiteServer | undefined;
let tracker: ReturnType<typeof startTracker> | undefined;
let workdir = "";

afterEach(async () => {
  await site?.stop();
  tracker?.server.stop(true);
  if (workdir) await rm(workdir, { recursive: true, force: true });
  site = tracker = undefined;
  workdir = "";
});

describe.skipIf(!hasChromium())("accuracy A/B browser driver (Chromium not installed in ~/.cache/ms-playwright)", () => {
  test("runs page, SPA, revenue, outbound, and download traffic with visitor headers", async () => {
    tracker = startTracker();
    site = startSite({
      port: 0,
      plausibleURL: tracker.server.url.origin,
      plausibleDomain: "site1.test",
      supermetryScriptURL: `${tracker.server.url.origin}/js/test.js`,
    });
    workdir = await mkdtemp(join(tmpdir(), "accuracy-driver-"));
    const reportPath = join(workdir, "report.json");

    const result = await runDriver({ scenario, outputPath: reportPath, siteURL: site.url.replace("127.0.0.1", "app.test"), concurrency: 2, speed: 100 });

    expect(result.exitCode).toBe(0);
    expect(result.report.totals).toEqual({ expected: 10, plausible_requests: 10, supermetry_requests: 10, failures: 0 });
    expect(JSON.parse(await readFile(reportPath, "utf8"))).toEqual(result.report);
    const expectedNames = new Map([
      ["198.51.100.11", ["pageview"]],
      ["198.51.100.12", ["pageview", "pageview"]],
      ["198.51.100.13", ["pageview", "pageview", "pageview"]],
      ["198.51.100.14", ["pageview", "Purchase", "File Download", "Outbound Link: Click"]],
    ]);
    for (const visit of scenario.visits) {
      const requests = tracker.seen.filter((request) => request.ip === visit.ip);
      expect(requests.every((request) => request.userAgent === visit.user_agent)).toBe(true);
      expect(new Set(requests.map((request) => request.endpoint))).toEqual(new Set<Seen["endpoint"]>(["plausible", "supermetry"]));
      for (const endpoint of ["plausible", "supermetry"] as const) {
        expect(requests.filter((request) => request.endpoint === endpoint).map((request) => request.name)).toEqual(expectedNames.get(visit.ip)!);
      }
    }
    expect(tracker.seen.filter((request) => request.name === "Purchase").map((request) => request.revenue)).toEqual([
      { amount: 49, currency: "USD" },
      { amount: 49, currency: "USD" },
    ]);
    expect(tracker.scriptLoads.get("198.51.100.13")).toBe(2);
  }, 30_000);

  test("fails loudly when tracker scripts do not fire", async () => {
    tracker = startTracker(false);
    site = startSite({
      port: 0,
      plausibleURL: tracker.server.url.origin,
      plausibleDomain: "site1.test",
      supermetryScriptURL: `${tracker.server.url.origin}/js/test.js`,
    });
    workdir = await mkdtemp(join(tmpdir(), "accuracy-driver-"));

    const result = await runDriver({ scenario, outputPath: join(workdir, "report.json"), siteURL: site.url.replace("127.0.0.1", "app.test"), concurrency: 2, speed: 100 });

    expect(result.exitCode).toBe(1);
    expect(result.report.visits.flatMap((visit) => visit.failures)).toContain(trackerFailure);
  }, 30_000);
});
