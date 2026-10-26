import { experimental_AstroContainer as AstroContainer } from "astro/container";
import { afterAll, describe, expect, test } from "bun:test";
import { rm } from "node:fs/promises";
import { dirname, join } from "node:path";

const root = join(import.meta.dir, "..");
const compiled = join(root, ".component-test.ts");
const compiler = Bun.resolveSync("@astrojs/compiler", dirname(Bun.resolveSync("astro/package.json", import.meta.dir)));

afterAll(() => rm(compiled, { force: true }));

async function component() {
  const source = await Bun.file(join(root, "Supermetry.astro")).text();
  const { transform } = (await import(compiler)) as typeof import("@astrojs/compiler");
  const result = await transform(source, { filename: "Supermetry.astro", internalURL: "astro/compiler-runtime" });
  expect(result.diagnostics.filter((diagnostic) => diagnostic.severity === 1)).toEqual([]);
  // Astro 5 strips compiler metadata during its own build; the container runtime lacks createMetadata.
  const code = result.code
    .replace(/,\s*createMetadata as \$\$createMetadata/, "")
    .replace(/^export const \$\$metadata = .*$/m, "");
  await Bun.write(compiled, code);
  return (await import(compiled)).default;
}

describe("<Supermetry />", () => {
  test("renders the tracker script and the View Transitions hook", async () => {
    const Supermetry = await component();
    const container = await AstroContainer.create();

    const plain = await container.renderToString(Supermetry, { props: { site: "site-id", host: "https://analytics.supermetry.com" } });
    expect(plain).toContain('src="https://analytics.supermetry.com/js/script.js"');
    expect(plain).toContain('data-site="site-id"');
    expect(plain).not.toContain("astro:page-load");


    const transitions = await container.renderToString(Supermetry, { props: { site: "site-id", viewTransitions: true } });
    expect(transitions).toContain('src="https://app.supermetry.com/js/script.full.js"');
    expect(transitions).toContain('data-manual="true"');
    expect(transitions).toContain('data-astro-transition-persist="supermetry"');
    expect(transitions).toContain("astro:page-load");
  });
});
