const result = await Bun.build({
  entrypoints: [import.meta.dir + "/src/replay.ts"],
  outdir: import.meta.dir + "/dist",
  naming: "replay.js",
  format: "iife",
  minify: true,
  target: "browser",
  sourcemap: "none",
});

if (!result.success) {
  for (const log of result.logs) console.error(log);
  process.exit(1);
}
