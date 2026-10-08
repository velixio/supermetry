const assert = require("node:assert/strict");
const fs = require("node:fs");
const vm = require("node:vm");

const source = fs.readFileSync(`${__dirname}/tracker.js`, "utf8");

function loadTracker() {
  const events = new Map();
  const payloads = [];
  const forbidStorage = () => {
    throw new Error("tracker accessed browser storage");
  };
  const document = {
    body: {scrollHeight: 1000},
    currentScript: {
      dataset: {site: "site-1"},
      hasAttribute: () => false,
    },
    documentElement: {scrollHeight: 1000},
    hidden: false,
    referrer: "https://referrer.example/",
    addEventListener(name, listener) {
      events.set(`document:${name}`, listener);
    },
  };
  Object.defineProperty(document, "cookie", {get: forbidStorage, set: forbidStorage});
  const window = {};
  Object.defineProperties(window, {
    localStorage: {get: forbidStorage},
    sessionStorage: {get: forbidStorage},
  });
  const context = {
    Blob: class Blob {
      constructor(parts) {
        this.parts = parts;
      }
    },
    Date: {now: () => 1000},
    Math,
    Object,
    JSON,
    addEventListener(name, listener) {
      events.set(`window:${name}`, listener);
    },
    document,
    fetch() {
      throw new Error("sendBeacon unexpectedly fell back to fetch");
    },
    history: {pushState() {}},
    innerHeight: 500,
    location: {hostname: "example.com", href: "https://example.com/start"},
    navigator: {
      webdriver: false,
      sendBeacon(_endpoint, body) {
        payloads.push(JSON.parse(body.parts.join("")));
        return true;
      },
    },
    screen: {width: 1440},
    scrollY: 0,
    window,
  };
  vm.runInNewContext(source, context, {filename: "tracker.js"});
  return {context, events, panel: window.panel, payloads};
}

function testIdentifyAndSubsequentEvents() {
  const tracker = loadTracker();
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview"]);
  assert.equal(tracker.payloads[0].profile_id, undefined);

  tracker.panel.identify("customer-42", {plan: "pro", region: "eu"});
  tracker.panel.track("Signup", {source: "hero"});
  tracker.events.get("window:scroll")();
  tracker.events.get("window:pagehide")();
  tracker.context.location.href = "https://example.com/next";
  tracker.context.history.pushState({}, "", "/next");

  assert.deepEqual(tracker.payloads.map((payload) => payload.name), [
    "pageview", "identify", "Signup", "engagement", "pageview",
  ]);
  assert.deepEqual(tracker.payloads[1].traits, {plan: "pro", region: "eu"});
  for (const payload of tracker.payloads.slice(1)) {
    assert.equal(payload.profile_id, "customer-42", `${payload.name} profile_id`);
  }
}

function testClear() {
  const tracker = loadTracker();
  tracker.panel.identify("customer-42", {});
  const beforeClear = tracker.payloads.length;
  tracker.panel.identify(null);
  assert.equal(tracker.payloads.length, beforeClear, "clearing must not send an event");
  tracker.panel.track("Signed out");
  assert.equal(tracker.payloads.at(-1).profile_id, undefined);
}

function testOmittedTraits() {
  const tracker = loadTracker();
  tracker.panel.identify("customer-42");
  assert.deepEqual(tracker.payloads.at(-1).traits, {});
  assert.equal(tracker.payloads.at(-1).profile_id, "customer-42");
}

function testValidationBoundaries() {
  const validTraits = Object.fromEntries(
    Array.from({length: 20}, (_, index) => [`key-${index}`, "v"]),
  );
  for (const [name, id, traits, valid] of [
    ["one-character id", "a", {}, true],
    ["128-character id", "a".repeat(128), validTraits, true],
    ["empty id", "", {}, false],
    ["129-character id", "a".repeat(129), {}, false],
    ["non-string id", 42, {}, false],
    ["boxed string id", new String("a"), {}, false],
    ["array traits", "a", [], false],
    ["function traits", "a", () => {}, false],
    ["null traits", "a", null, false],
    ["21 traits", "a", {...validTraits, overflow: "v"}, false],
    ["65-character key", "a", {[("k").repeat(65)]: "v"}, false],
    ["empty key", "a", {"": "v"}, false],
    ["constructor key", "a", {constructor: "value"}, true],
    ["257-character value", "a", {key: "v".repeat(257)}, false],
    ["non-string value", "a", {key: 1}, false],
    ["nested value", "a", {key: {nested: "v"}}, false],
  ]) {
    const tracker = loadTracker();
    tracker.panel.identify(id, traits);
    assert.equal(tracker.payloads.length, valid ? 2 : 1, name);
    tracker.panel.track("After identify");
    assert.equal(tracker.payloads.at(-1).profile_id, valid ? id : undefined, name);
  }
}

testIdentifyAndSubsequentEvents();
testClear();
testOmittedTraits();
testValidationBoundaries();
