import { describe, expect, test } from "bun:test";
import { buildFrames, parseV8Stack, scrubFrames } from "../src/frames";

const V8 = `Error: boom
    at doThing (/home/alice/proj/src/app/thing.js:10:5)
    at async run (file:///Users/Bob/proj/src/main.js:22:3)
    at Object.<anonymous> (/opt/build/a/b/c/lib/util.js:7:1)
    at /srv/x/y/z/w/anon.js:3:9
    at helper (/home/alice/proj/node_modules/lib/index.js:1:1)
    at node:internal/main/run_main_module:28:49
    at win (C:\\Users\\Carol\\app\\src\\win.js:4:2)`;

describe("frames", () => {
  test("parses V8 stacks and keeps first-party frames, scrubbed", () => {
    expect(buildFrames(parseV8Stack(V8))).toBe(
      [
        "~/proj/src/app/thing.js:10 doThing",
        "~/proj/src/main.js:22 async run",
        "c/lib/util.js:7 Object.<anonymous>",
        "z/w/anon.js:3 <anonymous>",
        "~/app/src/win.js:4 win",
      ].join("\n"),
    );
  });

  test("the message is never part of the parsed frames", () => {
    const stack = "Error: at fake (/home/a/secret.js:1:1)\n    at real (/home/a/x.js:2:2)";
    const out = buildFrames(parseV8Stack(stack, "at fake (/home/a/secret.js:1:1)"));
    expect(out).toBe("~/x.js:2 real");
  });

  test("scrubFrames golden", () => {
    expect(scrubFrames("file:///home/Bob/a/b.js:3 f")).toBe("~/a/b.js:3 f");
    expect(scrubFrames("/usr/lib/x/y/z/q.js:3 f")).toBe("y/z/q.js:3 f");
    expect(scrubFrames("src/rel.js:3 f")).toBe("src/rel.js:3 f");
    expect(scrubFrames("C:\\Users\\Dan\\p\\q.js:9 g")).toBe("~/p/q.js:9 g");
    expect(scrubFrames("D:\\build\\agent\\work\\src\\q.js:9 g")).toBe("work/src/q.js:9 g");
  });

  test("custom inApp overrides the default filter", () => {
    const frames = parseV8Stack("E\n    at a (/x/node_modules/m/i.js:1:1)\n    at b (/x/src/j.js:2:2)");
    expect(buildFrames(frames, (p) => p.includes("node_modules"))).toBe("node_modules/m/i.js:1 a");
  });

  test("caps at 4 KB on a line boundary", () => {
    const lines = Array.from({ length: 400 }, (_, i) => `src/f${i}.js:${i} fn${i}`);
    const out = scrubFrames(lines.join("\n"), 4096);
    expect(new TextEncoder().encode(out).length).toBeLessThanOrEqual(4096);
    expect(out.endsWith("\n")).toBe(false);
    expect(lines.join("\n").startsWith(out)).toBe(true);
    expect(out.split("\n").at(-1)!).toMatch(/^src\/f\d+\.js:\d+ fn\d+$/);
  });
});
