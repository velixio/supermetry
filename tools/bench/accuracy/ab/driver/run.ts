import { readFile, writeFile } from "node:fs/promises";
import { chromium } from "playwright";

import { runVisit, trackerFailure, type Visit, type VisitReport } from "./visit";

export type Scenario = { seed: number; visits: Visit[] };

export type DriverReport = {
  visits: VisitReport[];
  totals: { expected: number; plausible_requests: number; supermetry_requests: number; failures: number };
};

export type DriverOptions = {
  scenario: Scenario;
  outputPath: string;
  siteURL?: string;
  concurrency?: number;
  speed?: number;
};

function expectedEvents(scenario: Scenario): number {
  return scenario.visits.reduce(
    (total, visit) => total + visit.steps.filter((step) => step.kind !== "wait").length,
    0,
  );
}

function delayFor(visit: Visit, scenario: Scenario, speed: number): number {
  const maxOffset = Math.max(0, ...scenario.visits.map((item) => item.start_offset_ms));
  const scaledMaximum = maxOffset / speed;
  if (scaledMaximum <= 2_000) return visit.start_offset_ms / speed;
  return maxOffset === 0 ? 0 : visit.start_offset_ms / maxOffset * 2_000;
}

export async function runDriver(options: DriverOptions): Promise<{ exitCode: number; report: DriverReport }> {
  const siteURL = options.siteURL ?? "http://app.test:18091";
  const concurrency = Math.max(1, options.concurrency ?? 8);
  const speed = Math.max(Number.EPSILON, options.speed ?? 1);
  const browser = await chromium.launch({ headless: true, args: ["--host-resolver-rules=MAP *.test 127.0.0.1"] });
  const reports = new Array<VisitReport>(options.scenario.visits.length);
  let cursor = 0;
  try {
    await Promise.all(Array.from({ length: Math.min(concurrency, options.scenario.visits.length) }, async () => {
      while (cursor < options.scenario.visits.length) {
        const index = cursor++;
        const visit = options.scenario.visits[index]!;
        await new Promise((resolve) => setTimeout(resolve, delayFor(visit, options.scenario, speed)));
        reports[index] = await runVisit(browser, visit, siteURL);
      }
    }));
  } finally {
    await browser.close();
  }

  const expected = expectedEvents(options.scenario);
  const totals = reports.reduce(
    (result, visit) => ({
      expected,
      plausible_requests: result.plausible_requests + visit.plausible_requests,
      supermetry_requests: result.supermetry_requests + visit.supermetry_requests,
      failures: result.failures + visit.failures.length,
    }),
    { expected, plausible_requests: 0, supermetry_requests: 0, failures: 0 },
  );
  if (totals.plausible_requests < expected / 2 || totals.supermetry_requests < expected / 2) {
    for (const visit of reports) {
      if (!visit.failures.includes(trackerFailure)) visit.failures.push(trackerFailure);
      visit.ok = false;
    }
    totals.failures = reports.reduce((sum, visit) => sum + visit.failures.length, 0);
  }
  const report = { visits: reports, totals };
  await writeFile(options.outputPath, `${JSON.stringify(report, null, 2)}\n`);
  return { exitCode: totals.failures === 0 ? 0 : 1, report };
}

type CLIOptions = {
  visits?: string;
  out: string;
  concurrency: number;
  site: string;
  speed: number;
  dryRun: boolean;
};

function parseArgs(args: string[]): CLIOptions {
  const result: CLIOptions = { out: "driver-report.json", concurrency: 8, site: "http://app.test:18091", speed: 1, dryRun: false };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === "--dry-run") result.dryRun = true;
    else if (arg === "--visits") result.visits = args[++index];
    else if (arg === "--out") result.out = args[++index]!;
    else if (arg === "--concurrency") result.concurrency = Number(args[++index]);
    else if (arg === "--site") result.site = args[++index]!;
    else if (arg === "--speed") result.speed = Number(args[++index]);
    else throw new Error(`unknown argument: ${arg}`);
  }
  return result;
}

async function dryRun(siteURL: string): Promise<void> {
  const browser = await chromium.launch({ headless: true, args: ["--host-resolver-rules=MAP *.test 127.0.0.1"] });
  try {
    const page = await browser.newPage();
    const blocked = [process.env.PLAUSIBLE_URL, process.env.SUPERMETRY_SCRIPT_URL]
      .filter((value): value is string => Boolean(value))
      .map((value) => new URL(value).origin);
    await page.route("**/*", (route) => blocked.includes(new URL(route.request().url()).origin) ? route.abort() : route.continue());
    for (const path of ["/", "/about", "/pricing", "/blog/post-1", "/spa", "/spa/a", "/missing"]) {
      const response = await page.goto(new URL(path, siteURL).href, { waitUntil: "domcontentloaded" });
      const expected = path === "/missing" ? 404 : 200;
      if (response?.status() !== expected) throw new Error(`${path} returned HTTP ${response?.status() ?? "none"}`);
    }
  } finally {
    await browser.close();
  }
}

async function main(): Promise<number> {
  const options = parseArgs(process.argv.slice(2));
  if (options.dryRun) {
    await dryRun(options.site);
    return 0;
  }
  if (!options.visits) throw new Error("--visits is required unless --dry-run is used");
  const scenario = JSON.parse(await readFile(options.visits, "utf8")) as Scenario;
  const result = await runDriver({ scenario, outputPath: options.out, siteURL: options.site, concurrency: options.concurrency, speed: options.speed });
  return result.exitCode;
}

if (import.meta.main) {
  try {
    process.exitCode = await main();
  } catch (error) {
    console.error(error instanceof Error ? error.message : error);
    process.exitCode = 1;
  }
}
