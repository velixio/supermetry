import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const sdkRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const temporaryRoot = await mkdtemp(path.join(tmpdir(), "supermetry-sdk-packages-"));
const defaultPackages = ["js", "node", "react-native", "react", "vue", "astro", "express", "nextjs", "remix"];
const arguments_ = process.argv.slice(2);
const archiveOption = arguments_.indexOf("--archive");
let suppliedArchive;
if (archiveOption !== -1) {
  suppliedArchive = path.resolve(arguments_[archiveOption + 1] ?? "");
  arguments_.splice(archiveOption, 2);
  assert(arguments_.length === 1, "--archive requires exactly one package directory");
}
const packages = arguments_.length > 0 ? arguments_ : defaultPackages;

function run(command, args, cwd = sdkRoot) {
  const result = spawnSync(command, args, { cwd, encoding: "utf8" });
  if (result.status !== 0) {
    throw new Error([
      `${command} ${args.join(" ")} exited with ${result.status}`,
      result.stdout,
      result.stderr,
    ].filter(Boolean).join("\n"));
  }
  return result.stdout;
}

function exportTargets(value) {
  if (typeof value === "string") return value.startsWith("./") ? [value] : [];
  if (!value || typeof value !== "object") return [];
  return Object.values(value).flatMap(exportTargets);
}

function archiveName(packageName, version) {
  return `${packageName.replace(/^@/, "").replace("/", "-")}-${version}.tgz`;
}

try {
  const archives = [];
  for (const directory of packages) {
    const packageRoot = path.join(sdkRoot, directory);
    const sourceManifest = JSON.parse(await readFile(path.join(packageRoot, "package.json"), "utf8"));
    const archive = suppliedArchive ?? path.join(temporaryRoot, archiveName(sourceManifest.name, sourceManifest.version));

    if (!suppliedArchive) {
      run("bun", ["pm", "pack", "--filename", archive, "--quiet"], packageRoot);
    }
    archives.push(archive);

    const entries = new Set(run("tar", ["-tzf", archive]).trim().split("\n"));
    for (const expected of ["package/package.json", "package/README.md", "package/LICENSE", "package/NOTICE"]) {
      assert(entries.has(expected), `${sourceManifest.name} is missing ${expected}`);
    }

    const extractRoot = path.join(temporaryRoot, `extract-${directory}`);
    await mkdir(extractRoot);
    run("tar", ["-xzf", archive, "-C", extractRoot]);
    const extractedRoot = path.join(extractRoot, "package");
    const manifest = JSON.parse(await readFile(path.join(extractedRoot, "package.json"), "utf8"));

    assert.equal(manifest.publishConfig?.access, "public", `${manifest.name} must publish publicly`);
    assert.equal(manifest.publishConfig?.provenance, true, `${manifest.name} must request provenance`);
    assert.equal(manifest.repository?.url, "git+https://github.com/velixio/supermetry.git");
    assert.equal(manifest.repository?.directory, `sdk/${directory}`);
    assert.equal(manifest.homepage, `https://github.com/velixio/supermetry/tree/main/sdk/${directory}#readme`);
    assert.equal(manifest.bugs?.url, "https://github.com/velixio/supermetry/issues");

    const dependencyGroups = [manifest.dependencies, manifest.optionalDependencies, manifest.peerDependencies];
    for (const dependencies of dependencyGroups) {
      for (const [name, range] of Object.entries(dependencies ?? {})) {
        assert(!/^(workspace:|file:|link:)/.test(range), `${manifest.name} has unpublishable ${name}: ${range}`);
      }
    }

    const targets = new Set([
      manifest.main,
      manifest.module,
      manifest.types,
      manifest["react-native"],
      ...exportTargets(manifest.exports),
    ].filter((target) => typeof target === "string" && target.startsWith("./")));
    for (const target of targets) {
      const entry = `package/${target.slice(2)}`;
      assert(entries.has(entry), `${manifest.name} export points to missing ${entry}`);
    }

    const readme = await readFile(path.join(extractedRoot, "README.md"), "utf8");
    assert(readme.length >= 500, `${manifest.name} README is unexpectedly short`);
  }

  const fixtureRoot = path.join(temporaryRoot, "fixture");
  await mkdir(fixtureRoot);
  await writeFile(path.join(fixtureRoot, "package.json"), JSON.stringify({ private: true }, null, 2));
  run("npm", ["install", "--ignore-scripts", "--legacy-peer-deps", "--no-audit", "--no-fund", ...archives], fixtureRoot);

  for (const directory of packages) {
    const manifest = JSON.parse(await readFile(path.join(sdkRoot, directory, "package.json"), "utf8"));
    const installedManifest = JSON.parse(await readFile(
      path.join(fixtureRoot, "node_modules", ...manifest.name.split("/"), "package.json"),
      "utf8",
    ));
    assert.equal(installedManifest.version, manifest.version);
  }

  process.stdout.write(`verified ${packages.length} packed SDK packages\n`);
} finally {
  await rm(temporaryRoot, { recursive: true, force: true });
}
