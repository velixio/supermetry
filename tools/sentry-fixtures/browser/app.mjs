// Usage: node app.mjs <case>   (needs dist/client.js: bun build client.mjs --outfile dist/client.js)
// Serves a tiny page, drives headless Chromium, then simulates the page being hidden.
import http from "node:http";
import fs from "node:fs";
import { chromium } from "playwright-core";

const which = process.argv[2];
const dsn = process.env.SENTRY_FIXTURE_DSN;
const bundle = fs.readFileSync(new URL("./dist/client.js", import.meta.url));
const page_html = `<!doctype html><meta charset=utf-8><title>fixture</title>
<button id=btn>go</button><script>window.__DSN__=${JSON.stringify(dsn)}</script>
<script src="/client.js"></script>`;
const server = http.createServer((req, res) => {
  if (req.url.startsWith("/client.js")) return res.writeHead(200, { "content-type": "text/javascript" }).end(bundle);
  if (req.url.startsWith("/ping")) return res.end("pong");
  res.writeHead(200, { "content-type": "text/html" }).end(page_html);
});
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const port = server.address().port;

const browser = await chromium.launch();
const page = await browser.newPage();
await page.goto(`http://127.0.0.1:${port}/#${which}`);
await page.waitForTimeout(500);
await page.evaluate(() => window.__run());
await page.waitForTimeout(1500);
await page.evaluate(() => {
  Object.defineProperty(document, "visibilityState", { value: "hidden", configurable: true });
  document.dispatchEvent(new Event("visibilitychange"));
});
await page.waitForTimeout(1000);
await browser.close();
server.close();
