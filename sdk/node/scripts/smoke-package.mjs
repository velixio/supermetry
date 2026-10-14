import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, mkdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const packageRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const temporaryRoot = await mkdtemp(path.join(tmpdir(), "supermetry-node-package-"));

function run(command, args, cwd = packageRoot, stdio = "pipe") {
  const result = spawnSync(command, args, { cwd, encoding: "utf8", stdio });
  if (result.status !== 0) {
    throw new Error([
      `${command} ${args.join(" ")} exited with ${result.status}`,
      result.stdout,
      result.stderr,
    ].filter(Boolean).join("\n"));
  }
  return result.stdout;
}

try {
  const sourceManifest = JSON.parse(await readFile(path.join(packageRoot, "package.json"), "utf8"));
  const archive = process.argv[2]
    ? path.resolve(process.argv[2])
    : path.join(temporaryRoot, `supermetry-node-${sourceManifest.version}.tgz`);
  if (!process.argv[2]) run("npm", ["pack", "--pack-destination", temporaryRoot, "--silent"]);

  const entries = run("tar", ["-tzf", archive]).trim().split("\n");
  for (const expected of [
    "package/package.json",
    "package/README.md",
    "package/LICENSE",
    "package/NOTICE",
    "package/dist/index.js",
    "package/dist/index.cjs",
    "package/dist/index.d.ts",
    "package/dist/validate.d.ts",
  ]) {
    assert(entries.includes(expected), `packed package is missing ${expected}`);
  }

  run("tar", ["-xzf", archive, "-C", temporaryRoot]);
  const extracted = path.join(temporaryRoot, "package");
  const manifest = JSON.parse(await readFile(path.join(extracted, "package.json"), "utf8"));
  assert.equal(manifest.name, "@supermetry/node");

  const fixtureRoot = path.join(temporaryRoot, "fixture");
  const installedPackage = path.join(fixtureRoot, "node_modules", "@supermetry", "node");
  await mkdir(path.dirname(installedPackage), { recursive: true });
  await rename(extracted, installedPackage);
  await writeFile(path.join(fixtureRoot, "index.mjs"), [
    'import { Supermetry, VERSION } from "@supermetry/node";',
    `if (typeof Supermetry !== "function" || VERSION !== "${manifest.version}") process.exit(1);`,
    "",
  ].join("\n"));
  await writeFile(path.join(fixtureRoot, "index.cjs"), [
    'const { Supermetry, VERSION } = require("@supermetry/node");',
    `if (typeof Supermetry !== "function" || VERSION !== "${manifest.version}") process.exit(1);`,
    "",
  ].join("\n"));
  await writeFile(path.join(fixtureRoot, "index.ts"), [
    'import { Supermetry, type SendResult } from "@supermetry/node";',
    'const client = new Supermetry({ site: "site", host: "https://analytics.example.com", url: "https://example.com" });',
    'const result: Promise<SendResult> = client.track("Signup");',
    'void result;',
    "",
  ].join("\n"));
  run("node", ["index.mjs"], fixtureRoot);
  run("node", ["index.cjs"], fixtureRoot);
  run(path.join(packageRoot, "node_modules", ".bin", "tsc"), [
    "--noEmit",
    "--strict",
    "--target", "ES2022",
    "--module", "NodeNext",
    "--moduleResolution", "NodeNext",
    "index.ts",
  ], fixtureRoot, "inherit");

  process.stdout.write(`verified @supermetry/node@${manifest.version} package\n`);
} finally {
  await rm(temporaryRoot, { recursive: true, force: true });
}
