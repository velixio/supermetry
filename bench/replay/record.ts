// Throwaway: record rrweb sessions on real pages with scripted activity, report sizes.
// Usage: bun run record.ts [durationSec=90]
import { chromium, type Page } from "playwright";
import { gzipSync } from "node:zlib";
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";

const DURATION = Number(process.argv[2] ?? 90) * 1000;
const RRWEB = readFileSync(new URL("./node_modules/rrweb/dist/rrweb.umd.min.cjs", import.meta.url), "utf8");
const RRWEB_VERSION = JSON.parse(readFileSync(new URL("./node_modules/rrweb/package.json", import.meta.url), "utf8")).version;

const PAGES: Record<string, string> = {
  wikipedia: "https://en.wikipedia.org/wiki/Web_analytics",
  mdn: "https://developer.mozilla.org/en-US/docs/Web/API/Fetch_API/Using_Fetch",
  github: "https://github.com/explore",
};

const CONFIGS: Record<string, string> = {
  defaults: `{}`,
  lean: `{
    sampling: { mousemove: 100, scroll: 150, input: 'last' },
    recordCanvas: false, inlineImages: false, collectFonts: false,
    maskAllInputs: false, maskInputOptions: { password: true },
    checkoutEveryNms: 300000 }`,
  "lean-nomouse": `{
    sampling: { mousemove: false, scroll: 150, input: 'last' },
    recordCanvas: false, inlineImages: false, collectFonts: false,
    maskAllInputs: false, maskInputOptions: { password: true },
    checkoutEveryNms: 300000 }`,
};

function rng(seed: number) {
  return () => ((seed = (seed * 1664525 + 1013904223) >>> 0) / 2 ** 32);
}

async function activity(page: Page, durationMs: number) {
  const r = rng(42);
  const vw = 1280, vh = 800;
  const t0 = Date.now();
  let i = 0;
  while (Date.now() - t0 < durationMs) {
    i++;
    // mouse wander
    await page.mouse.move(r() * vw, r() * vh, { steps: 15 + Math.floor(r() * 20) });
    // scroll (mostly down, sometimes back up)
    await page.mouse.wheel(0, i % 7 === 0 ? -900 : 250 + r() * 400);
    await page.waitForTimeout(300 + r() * 700);
    // click somewhere (anchors neutralised so we stay on page)
    if (i % 3 === 0) await page.mouse.click(100 + r() * (vw - 200), 150 + r() * (vh - 300));
    // typing every ~6 iterations
    if (i % 6 === 0) {
      await page.focus("#__bench_input").catch(() => {});
      await page.keyboard.type("session replay bench " + i, { delay: 60 });
    }
    await page.waitForTimeout(400 + r() * 800);
  }
}

async function runOne(name: string, url: string, cfgName: string) {
  const browser = await chromium.launch({ executablePath: process.env.CHROME ?? "/path/to/local/.cache/ms-playwright/chromium_headless_shell-1243/chrome-headless-shell-linux64/chrome-headless-shell" });
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 800 }, bypassCSP: true });
  const page = await ctx.newPage();
  await page.goto(url, { waitUntil: "load", timeout: 60000 });
  await page.waitForTimeout(1500);
  const domNodes = await page.evaluate(() => document.getElementsByTagName("*").length);
  await page.addScriptTag({ content: RRWEB });
  await page.evaluate(`(() => {
    // keep the session on one page
    document.addEventListener('click', e => { if (e.target.closest('a,button,[role=button]')) { e.preventDefault(); } }, true);
    // a text input to type into, and a dynamic list (simulated app updates)
    const box = document.createElement('div');
    box.style.cssText = 'position:fixed;bottom:8px;right:8px;z-index:99999;background:#fff;border:1px solid #888;padding:6px;width:260px;max-height:200px;overflow:auto;font:12px sans-serif';
    box.innerHTML = '<input id="__bench_input" type="text" style="width:240px"><input type="password" value="hunter2"><ul id="__bench_list"></ul>';
    document.body.appendChild(box);
    let n = 0;
    setInterval(() => {
      const ul = document.getElementById('__bench_list');
      const li = document.createElement('li'); li.textContent = 'update #' + (++n) + ' at ' + new Date().toISOString();
      ul.prepend(li); if (ul.children.length > 10) ul.lastChild.remove();
    }, 2000);
    window.__events = [];
    rrweb.record({ emit(e) { window.__events.push(e); }, ...${CONFIGS[cfgName]} });
  })()`);
  const start = Date.now();
  await activity(page, DURATION);
  const events: any[] = await page.evaluate("window.__events");
  const elapsed = (Date.now() - start) / 1000;
  await browser.close();

  const json = JSON.stringify(events);
  const raw = Buffer.byteLength(json);
  const gz = gzipSync(json, { level: 6 }).length;
  const full = events.filter((e) => e.type === 2);
  const firstFull = full.length ? Buffer.byteLength(JSON.stringify(full[0])) : 0;
  const firstFullGz = full.length ? gzipSync(JSON.stringify(full[0]), { level: 6 }).length : 0;
  // incremental only (everything except Meta/FullSnapshot)
  const incr = events.filter((e) => e.type !== 2 && e.type !== 4);
  const incrGz = gzipSync(JSON.stringify(incr), { level: 6 }).length;
  // realistic upload: batches flushed every 10 s, each gzipped separately
  const t0 = events[0]?.timestamp ?? 0;
  const batches = new Map<number, any[]>();
  for (const e of events) {
    const k = Math.floor((e.timestamp - t0) / 10000);
    (batches.get(k) ?? batches.set(k, []).get(k)!).push(e);
  }
  let chunkedGz = 0;
  for (const b of batches.values()) chunkedGz += gzipSync(JSON.stringify(b), { level: 6 }).length;
  const mins = elapsed / 60;
  mkdirSync(new URL("./out/", import.meta.url), { recursive: true });
  writeFileSync(new URL(`./out/${name}-${cfgName}.json`, import.meta.url), json);
  return {
    page: name, config: cfgName, domNodes, seconds: +elapsed.toFixed(1), events: events.length,
    rawKB: +(raw / 1024).toFixed(1), gzKB: +(gz / 1024).toFixed(1),
    firstFullKB: +(firstFull / 1024).toFixed(1), firstFullGzKB: +(firstFullGz / 1024).toFixed(1),
    incrGzKBperMin: +(incrGz / 1024 / mins).toFixed(1),
    chunked10sGzKB: +(chunkedGz / 1024).toFixed(1),
    gzKBperMin: +(gz / 1024 / mins).toFixed(1),
    chunkedGzKBperMin: +(chunkedGz / 1024 / mins).toFixed(1),
  };
}

mkdirSync(new URL("./out/", import.meta.url), { recursive: true });
const jobs = [];
for (const [name, url] of Object.entries(PAGES))
  for (const cfg of Object.keys(CONFIGS))
    jobs.push(runOne(name, url, cfg).catch((e) => ({ page: name, config: cfg, error: String(e).slice(0, 200) })));
const results = await Promise.all(jobs);
console.log("rrweb", RRWEB_VERSION);
console.table(results);
writeFileSync(new URL("./out/results.json", import.meta.url), JSON.stringify({ rrweb: RRWEB_VERSION, durationMs: DURATION, results }, null, 2));
