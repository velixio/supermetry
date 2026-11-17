// Generates no-app/custom-pixel.js from the web pixel's mapping module, so the
// paste-in custom pixel and the app extension send identical events.
// Usage: node scripts/build-custom-pixel.mjs [--check]
import { readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const mappingPath = path.join(root, "extensions/supermetry-pixel/src/mapping.js");
export const outputPath = path.join(root, "no-app/custom-pixel.js");

export async function render() {
  const mapping = (await readFile(mappingPath, "utf8"))
    .replace(/^\/\/.*\n(\/\/.*\n)*\n/, "")
    .replace(/^export /gm, "");
  return `// Supermetry custom pixel for Shopify (Settings → Customer events → Add custom pixel).
// Generated from extensions/supermetry-pixel/src/mapping.js by
// scripts/build-custom-pixel.mjs; do not edit by hand.
//
// Set your site ID below. pageviews: "checkout" when the theme snippet (or app
// embed) counts storefront pages, "all" when this pixel is the only tracker.
const SUPERMETRY = {
  host: "https://app.supermetry.com",
  site: "YOUR_SITE_ID",
  pageviews: "checkout",
};

${mapping.trim()}

for (const name of SUBSCRIBED_EVENTS) {
  analytics.subscribe(name, (event) => {
    const request = toRequest(event, SUPERMETRY);
    if (request) fetch(request.url, request.init).catch(() => {});
  });
}
`;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const content = await render();
  if (process.argv.includes("--check")) {
    const current = await readFile(outputPath, "utf8").catch(() => "");
    if (current !== content) {
      console.error("no-app/custom-pixel.js is out of date: run node scripts/build-custom-pixel.mjs");
      process.exit(1);
    }
  } else {
    await writeFile(outputPath, content);
  }
}
