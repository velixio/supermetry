// Post-processes the Vite build the way `sentry-cli sourcemaps inject` and the
// bundler plugin's upload step would, then writes the fixtures for
// internal/sourcemap/testdata/vite/. Nothing is uploaded.
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import zlib from "node:zlib";

const out = process.argv[2];
if (!out) throw new Error("usage: bun gen.mjs <outdir>");
mkdirSync(out, { recursive: true });

let js = readFileSync("dist/assets/app.js", "utf8");
const map = JSON.parse(readFileSync("dist/assets/app.js.map", "utf8"));
const id = /_sentryDebugIds\[r\]="([0-9a-f-]{36})"/.exec(js)?.[1];
if (!id) throw new Error("debug id not injected");
if (!js.includes("//# debugId=")) js = js.replace(/\n?$/, "\n") + `//# debugId=${id}\n`;
map.debugId = id;
const mapText = JSON.stringify(map);

// Position of the throw site in the minified file (0-based line/column).
const lines = js.split("\n");
let throwPos;
lines.forEach((l, i) => {
  const c = l.indexOf('throw new Error("cart limit exceeded")');
  if (c >= 0) throwPos = { line: i, col: c, newCol: c + "throw ".length };
});

function crc(buf) { return zlib.crc32(buf) >>> 0; }
function zip(entries, prefix) {
  const parts = [prefix];
  let off = prefix.length;
  const central = [];
  for (const [name, data] of entries) {
    const nb = Buffer.from(name);
    const comp = zlib.deflateRawSync(data);
    const h = Buffer.alloc(30);
    h.writeUInt32LE(0x04034b50, 0); h.writeUInt16LE(20, 4); h.writeUInt16LE(0x0800, 6);
    h.writeUInt16LE(8, 8); h.writeUInt32LE(0x21, 12) /* 1980-01-01 */;
    h.writeUInt32LE(crc(data), 14); h.writeUInt32LE(comp.length, 18);
    h.writeUInt32LE(data.length, 22); h.writeUInt16LE(nb.length, 26);
    const c = Buffer.alloc(46);
    c.writeUInt32LE(0x02014b50, 0); c.writeUInt16LE(20, 4); c.writeUInt16LE(20, 6);
    c.writeUInt16LE(0x0800, 8); c.writeUInt16LE(8, 10); c.writeUInt32LE(0x21, 14);
    c.writeUInt32LE(crc(data), 16); c.writeUInt32LE(comp.length, 20);
    c.writeUInt32LE(data.length, 24); c.writeUInt16LE(nb.length, 28); c.writeUInt32LE(off, 42);
    central.push(c, nb);
    parts.push(h, nb, comp);
    off += 30 + nb.length + comp.length;
  }
  const cd = Buffer.concat(central);
  const e = Buffer.alloc(22);
  e.writeUInt32LE(0x06054b50, 0); e.writeUInt16LE(entries.length, 8);
  e.writeUInt16LE(entries.length, 10); e.writeUInt32LE(cd.length, 12); e.writeUInt32LE(off, 16);
  parts.push(cd, e);
  return Buffer.concat(parts);
}

const sysb = Buffer.alloc(8);
sysb.write("SYSB", 0); sysb.writeUInt32LE(2, 4);
const manifest = {
  org: "fixture-org", project: "fixture-project", release: "fixture-1.0.0",
  files: {
    "_/_/assets/app.js": { url: "~/assets/app.js", type: "minified_source", headers: { "debug-id": id, Sourcemap: "app.js.map" } },
    "_/_/assets/app.js.map": { url: "~/assets/app.js.map", type: "source_map", headers: { "debug-id": id } },
  },
};
const bundle = zip([
  ["manifest.json", Buffer.from(JSON.stringify(manifest))],
  ["_/_/assets/app.js", Buffer.from(js)],
  ["_/_/assets/app.js.map", Buffer.from(mapText)],
], sysb);

writeFileSync(`${out}/app.js`, js);
writeFileSync(`${out}/app.js.map`, mapText);
writeFileSync(`${out}/bundle.zip`, bundle);
writeFileSync(`${out}/expected.json`, JSON.stringify({ debugId: id, minified: throwPos }, null, 2) + "\n");
console.log(JSON.stringify({ id, throwPos }));
