import type { Browser, BrowserContext, Page, Response } from "playwright";

export type StepKind = "goto" | "spa" | "hash" | "wait" | "event" | "outbound" | "download";

export type Step = {
  kind: StepKind;
  path: string;
  ms: number;
  name: string;
  props: Record<string, unknown>;
  revenue: number | null;
  currency: string;
  href: string;
};

export type Visit = {
  id: string;
  ip: string;
  user_agent: string;
  viewport: [number, number];
  device: "desktop" | "mobile" | "tablet";
  browser: string;
  os: string;
  referrer: string;
  utm: Record<string, string>;
  start_offset_ms: number;
  steps: Step[];
};

export type VisitReport = {
  id: string;
  ok: boolean;
  plausible_requests: number;
  supermetry_requests: number;
  failures: string[];
};

export const trackerFailure =
  "tracker did not fire: check navigator.webdriver, host name, Do-Not-Track, UA";

type Product = "plausible" | "supermetry";

function productFor(response: Response): Product | undefined {
  const path = new URL(response.url()).pathname;
  if (path === "/api/event") return "plausible";
  if (path === "/api/public/v1/events") return "supermetry";
  return undefined;
}

function selectorFor(value: string): string {
  return `a[href=${JSON.stringify(value)}]`;
}

async function performStep(page: Page, step: Step, siteURL: string, firstGoto: boolean, visit: Visit): Promise<void> {
  switch (step.kind) {
    case "goto": {
      const url = new URL(step.path, siteURL);
      if (firstGoto) {
        // The scenario stores UTM keys without the prefix ("campaign"); the URL needs utm_*.
        for (const [key, value] of Object.entries(visit.utm)) {
          url.searchParams.set(key.startsWith("utm_") ? key : `utm_${key}`, value);
        }
      }
      await page.goto(url.href, {
        waitUntil: "domcontentloaded",
        ...(firstGoto && visit.referrer ? { referer: visit.referrer } : {}),
      });
      return;
    }
    case "spa":
      await page.click(selectorFor(step.path));
      return;
    case "hash":
      await page.evaluate((hash) => { location.hash = hash; }, step.path);
      return;
    case "wait":
      await page.waitForTimeout(step.ms);
      return;
    case "event":
      await page.evaluate(({ name, props, revenue, currency }) => {
        window.__track?.(name, props, revenue === null ? undefined : { amount: revenue, currency });
      }, step);
      return;
    case "outbound":
      await page.route(step.href, async (route) => {
        await new Promise((resolve) => setTimeout(resolve, 300));
        await route.fulfill({ status: 200, contentType: "text/html", body: "<!doctype html><title>ok</title>ok" });
      });
      await Promise.all([
        page.waitForLoadState("domcontentloaded").catch(() => undefined),
        page.click(selectorFor(step.href)),
      ]);
      return;
    case "download": {
      const href = step.href || "/files/report.pdf";
      const [download] = await Promise.all([
        page.waitForEvent("download"),
        page.click(selectorFor(href)),
      ]);
      await download.cancel();
    }
  }
}

declare global {
  interface Window {
    __track?: (name: string, props: Record<string, unknown>, revenue?: { amount: number; currency: string }) => void;
  }
}

async function waitForTrackers(counts: Record<Product, number>, before: Record<Product, number>): Promise<boolean> {
  const deadline = Date.now() + 3_000;
  while (Date.now() < deadline) {
    if (counts.plausible > before.plausible && counts.supermetry > before.supermetry) {
      await new Promise((resolve) => setTimeout(resolve, 300));
      return true;
    }
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  return false;
}

export async function runVisit(browser: Browser, visit: Visit, siteURL: string): Promise<VisitReport> {
  const context: BrowserContext = await browser.newContext({
    userAgent: visit.user_agent,
    viewport: { width: visit.viewport[0], height: visit.viewport[1] },
    extraHTTPHeaders: { "X-Forwarded-For": visit.ip },
    locale: "en-US",
    timezoneId: "UTC",
    deviceScaleFactor: visit.device === "desktop" ? 1 : 2,
    isMobile: visit.device === "mobile",
    hasTouch: visit.device !== "desktop",
    acceptDownloads: true,
  });
  await context.addInitScript(() => {
    Object.defineProperty(navigator, "webdriver", { configurable: true, get: () => false });
  });
  await context.route("**/*", async (route) => {
    await route.continue({ headers: { ...route.request().headers(), "x-forwarded-for": visit.ip } });
  });

  const page = await context.newPage();
  const counts: Record<Product, number> = { plausible: 0, supermetry: 0 };
  const failures: string[] = [];
  page.on("response", (response) => {
    const product = productFor(response);
    if (!product) return;
    counts[product] += 1;
    if (response.status() >= 400) failures.push(`${product} tracker returned HTTP ${response.status()}`);
  });

  let firstGoto = true;
  let currentPath: string | null = null;
  try {
    for (const step of visit.steps) {
      // A single-page-app navigation to the page the visitor is already on is not a
      // pageview in Plausible (which ignores it) and is not counted by the truth model.
      const repeatedSpa = step.kind === "spa" && step.path === currentPath;
      if (step.kind === "goto" || step.kind === "spa") currentPath = step.path;
      const expectsTracking = step.kind !== "wait" && !repeatedSpa;
      const before = { ...counts };
      await performStep(page, step, siteURL, firstGoto, visit);
      if (step.kind === "goto") firstGoto = false;
      if (expectsTracking && !(await waitForTrackers(counts, before))) {
        failures.push(trackerFailure);
        break;
      }
    }
  } catch (error) {
    failures.push(error instanceof Error ? error.message : String(error));
  } finally {
    await context.close();
  }

  return {
    id: visit.id,
    ok: failures.length === 0,
    plausible_requests: counts.plausible,
    supermetry_requests: counts.supermetry,
    failures,
  };
}
