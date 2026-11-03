import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import vm from "node:vm";

const read = (path) => readFileSync(new URL(`../${path}`, import.meta.url), "utf8");
const source = read("member-events.js");

function run(search, { loadAfter = 0 } = {}) {
  const tracked = [];
  const timers = [];
  const window = {};
  const context = {
    window,
    location: { search },
    URLSearchParams,
    setTimeout: (fn) => timers.push(fn),
  };
  if (loadAfter === 0) window.supermetry = { track: (name, props) => tracked.push([name, props]) };
  vm.runInNewContext(source, context);
  for (let tick = 1; timers.length && tick < 200; tick++) {
    if (tick === loadAfter) window.supermetry = { track: (name, props) => tracked.push([name, props]) };
    timers.shift()();
  }
  return JSON.parse(JSON.stringify(tracked));
}

test("snippets embed member-events.js verbatim", () => {
  assert.ok(read("code-injection.html").includes(`<script>\n${source}</script>`));
  assert.ok(read("partials/supermetry.hbs").includes(`<script>\n${source}</script>`));
});

test("Portal signup confirmation fires Signup", () => {
  for (const action of ["signup", "signup-paid", "subscribe"]) {
    assert.deepEqual(run(`?action=${action}&success=true`), [["Signup", { source: "ghost_portal" }]]);
  }
  assert.deepEqual(run("?action=signin&success=true"), []);
  assert.deepEqual(run("?action=signup&success=false"), []);
  assert.deepEqual(run(""), []);
});

test("Stripe checkout success fires Subscription", () => {
  assert.deepEqual(run("?stripe=success"), [["Subscription", { source: "ghost_portal" }]]);
  assert.deepEqual(run("?stripe=cancel"), []);
});

test("waits for the deferred tracker, then gives up after ~10s", () => {
  assert.deepEqual(run("?stripe=success", { loadAfter: 5 }), [["Subscription", { source: "ghost_portal" }]]);
  assert.deepEqual(run("?stripe=success", { loadAfter: -1 }), []);
});
