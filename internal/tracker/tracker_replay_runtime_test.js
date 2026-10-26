const assert = require("node:assert/strict");
const fs = require("node:fs");
const vm = require("node:vm");

const source = fs.readFileSync(`${__dirname}/tracker.replay.js`, "utf8");

// Runs the replay loader with the given data-api value and returns where it
// loads replay.js from and the base the recorder posts to.
function load(api, href = "https://supermetry.com/page") {
  const appended = [];
  const dataset = {site: "abc123def456", replay: "always"};
  if (api !== undefined) dataset.api = api;
  const supermetry = {_allowed: () => true, _privacyState: true};
  const document = {
    currentScript: {dataset},
    createElement: () => ({}),
    head: {appendChild: (node) => appended.push(node)},
  };
  vm.runInNewContext(source, {
    window: {supermetry},
    document,
    navigator: {},
    location: {href},
    URL,
  });
  assert.equal(appended.length, 1, `replay.js not requested for data-api=${api}`);
  return {src: appended[0].src, base: supermetry._replay.o};
}

// Default and direct events endpoints load replay from the origin.
assert.deepEqual(load(undefined), {src: "https://supermetry.com/replay.js", base: "https://supermetry.com"});
assert.deepEqual(load("https://app.supermetry.com/api/public/v1/events"), {src: "https://app.supermetry.com/replay.js", base: "https://app.supermetry.com"});

// A first-party proxy under a path prefix keeps the prefix.
assert.deepEqual(load("/sm/api/public/v1/events"), {src: "https://supermetry.com/sm/replay.js", base: "https://supermetry.com/sm"});
assert.deepEqual(load("https://supermetry.com/a/b/api/public/v1/events"), {src: "https://supermetry.com/a/b/replay.js", base: "https://supermetry.com/a/b"});
assert.deepEqual(load("/sm/api/public/v1/events/"), {src: "https://supermetry.com/sm/replay.js", base: "https://supermetry.com/sm"});

// A custom event path (SUPERMETRY_EVENT_PATH) has no known suffix: fall back to the origin.
assert.deepEqual(load("/a/e"), {src: "https://supermetry.com/replay.js", base: "https://supermetry.com"});

console.log("tracker replay runtime contract ok");
