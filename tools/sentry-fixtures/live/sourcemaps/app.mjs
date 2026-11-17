// Usage: node app.mjs <dist dir>  (env SENTRY_FIXTURE_DSN, SM_RELEASE)
// Serves the built app, loads it in headless Chromium and lets it throw.
import http from "node:http";
import fs from "node:fs";
import path from "node:path";
import { chromium } from "playwright-core";

const dist = path.resolve(process.argv[2]);
const dsn = process.env.SENTRY_FIXTURE_DSN, release = process.env.SM_RELEASE;
const html = `<!doctype html><meta charset=utf-8><title>fixture</title>
<script>window.__DSN__=${JSON.stringify(dsn)};window.__RELEASE__=${JSON.stringify(release)}</script>
<script type="module" src="/assets/app.js"></script>`;
const server = http.createServer((req, res) => {
  const p = req.url.split("?")[0];
  if (p.startsWith("/assets/")) {
    const f = path.join(dist, p);
    if (f.startsWith(dist) && fs.existsSync(f)) return res.writeHead(200, { "content-type": "text/javascript" }).end(fs.readFileSync(f));
    return res.writeHead(404).end();
  }
  res.writeHead(200, { "content-type": "text/html" }).end(html);
});
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const browser = await chromium.launch();
const page = await browser.newPage();
await page.goto(`http://127.0.0.1:${server.address().port}/`);
await page.waitForTimeout(500);
await page.evaluate(() => window.__run());
await page.waitForTimeout(2500);
await browser.close();
server.close();
