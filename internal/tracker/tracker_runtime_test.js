const assert = require("node:assert/strict");
const fs = require("node:fs");
const vm = require("node:vm");

const source = fs.readFileSync(`${__dirname}/tracker.js`, "utf8");

function buildSource(full) {
  return source.replace(/\/\*@full\*\/([\s\S]*?)\/\*@end\*\//g, full ? "$1" : "");
}

function loadTracker({full = false, dataset = {}} = {}) {
  const events = new Map();
  const payloads = [];
  let now = 1000;
  const forbidStorage = () => {
    throw new Error("tracker accessed browser storage");
  };
  const location = {hostname: "example.com", href: "https://example.com/start"};
  const document = {
    body: {scrollHeight: 1000},
    currentScript: {
      dataset: {site: "site-1", ...dataset},
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
  const history = {
    pushState(_state, _unused, url) {
      if (url) location.href = new URL(url, location.href).href;
    },
    replaceState(_state, _unused, url) {
      if (url) location.href = new URL(url, location.href).href;
    },
  };
  const context = {
    Blob: class Blob {
      constructor(parts) {
        this.parts = parts;
      }
    },
    Date: {now: () => now += 100},
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
    history,
    innerHeight: 500,
    location,
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
  vm.runInNewContext(buildSource(full), context, {filename: "tracker.js"});
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

function testManualPageviews() {
  const tracker = loadTracker({full: true, dataset: {manual: "true"}});
  assert.deepEqual(tracker.payloads, [], "manual mode must suppress the initial pageview");

  tracker.context.history.pushState({}, "", "/automatic");
  assert.deepEqual(tracker.payloads, [], "manual mode must suppress pushState pageviews");

  tracker.panel.pageview({url: "https://example.com/a", props: {k: "v"}});
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["engagement", "pageview"]);
  assert.equal(tracker.payloads[1].url, "https://example.com/a");
  assert.deepEqual(tracker.payloads[1].props, {k: "v"});
}

function testHashRouting() {
  const enabled = loadTracker({full: true, dataset: {hash: "true"}});
  enabled.context.location.href = "https://example.com/start#b";
  enabled.events.get("window:hashchange")();
  assert.deepEqual(enabled.payloads.map((payload) => payload.name), [
    "pageview", "engagement", "pageview",
  ]);
  assert.equal(enabled.payloads.at(-1).url, "https://example.com/start#b");

  const disabled = loadTracker({full: true});
  disabled.context.location.href = "https://example.com/start#b";
  assert.equal(disabled.events.has("window:hashchange"), false);
  assert.deepEqual(disabled.payloads.map((payload) => payload.name), ["pageview"]);
}

function testReplaceStateRouting() {
  const tracker = loadTracker({full: true});
  tracker.context.history.replaceState({}, "", "/start");
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview"]);

  tracker.context.history.replaceState({}, "", "/next?tab=one");
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), [
    "pageview", "engagement", "pageview",
  ]);
  assert.equal(tracker.payloads.at(-1).url, "https://example.com/next?tab=one");
}

function testDuplicateNavigationIsDropped() {
  const tracker = loadTracker({full: true, dataset: {hash: "true"}});
  tracker.context.location.href = "https://example.com/next#details";
  tracker.events.get("window:hashchange")();
  tracker.events.get("window:popstate")();
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), [
    "pageview", "engagement", "pageview",
  ]);
}

function testBeforeSend() {
  const edited = loadTracker({full: true});
  edited.panel.beforeSend = (event) => ({...event, props: {x: "1"}, site: "other-site"});
  edited.panel.track("Edited", {ignored: "yes"});
  assert.deepEqual(edited.payloads.at(-1).props, {x: "1"});
  assert.equal(edited.payloads.at(-1).site, "site-1");

  for (const [name, hook] of [
    ["null", () => null],
    ["throw", () => { throw new Error("redaction failed"); }],
    ["string", () => "yes"],
    ["boolean", () => true],
  ]) {
    const tracker = loadTracker({full: true});
    tracker.panel.beforeSend = hook;
    const before = tracker.payloads.length;
    tracker.panel.track("Dropped");
    assert.equal(tracker.payloads.length, before, `${name} return must fail closed`);
  }
}

function testNamedBeforeSendIsResolvedAtSendTime() {
  const tracker = loadTracker({full: true, dataset: {beforeSend: "hook"}});
  tracker.context.window.hook = (event) => ({...event, props: {version: "first"}});
  tracker.panel.track("First");
  tracker.context.window.hook = (event) => ({...event, props: {version: "second"}});
  tracker.panel.track("Second");
  assert.deepEqual(tracker.payloads.slice(-2).map((payload) => payload.props), [
    {version: "first"}, {version: "second"},
  ]);
}

function testConsentGrantFlushesInOrderThroughBeforeSend() {
  const tracker = loadTracker({full: true, dataset: {consent: "required"}});
  tracker.panel.beforeSend = (event) => ({...event, props: {approved: event.name}});
  tracker.panel.track("A");

  assert.deepEqual(tracker.payloads, [], "consent mode must start paused");
  tracker.panel.consent(true);

  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview", "A"]);
  assert.deepEqual(tracker.payloads.map((payload) => payload.props.approved), ["pageview", "A"]);
}

function testConsentQueueKeepsNewestTwentyEvents() {
  const tracker = loadTracker({full: true, dataset: {consent: "required", manual: "true"}});
  for (let index = 1; index <= 25; index++) {
    tracker.panel.track(`Event ${index}`);
  }

  tracker.panel.consent(true);

  assert.deepEqual(tracker.payloads.map((payload) => payload.name),
    Array.from({length: 20}, (_, index) => `Event ${index + 6}`));
}

function testConsentDenialClearsQueueAndDropsUntilGranted() {
  const tracker = loadTracker({full: true, dataset: {consent: "required", manual: "true"}});
  tracker.panel.track("Before denial");
  tracker.panel.consent(false);
  tracker.panel.track("While denied");
  tracker.panel.consent(true);

  assert.deepEqual(tracker.payloads, []);
  tracker.panel.track("After grant");
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["After grant"]);
}

function testConsentGrantIsIdempotent() {
  const tracker = loadTracker({full: true, dataset: {consent: "required"}});
  tracker.panel.consent(true);
  tracker.panel.consent(true);

  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview"]);
}

function testConsentQueuesManualPageview() {
  const tracker = loadTracker({full: true, dataset: {consent: "required", manual: "true"}});
  tracker.panel.pageview({url: "https://example.com/consented"});
  assert.deepEqual(tracker.payloads, []);

  tracker.panel.consent(true);
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["engagement", "pageview"]);
  assert.equal(tracker.payloads.at(-1).url, "https://example.com/consented");
}

function testConsentIsNoOpWithoutGate() {
  const tracker = loadTracker({full: true});
  tracker.panel.consent(false);
  tracker.panel.track("Still enabled");

  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview", "Still enabled"]);
}

function testCoreBuildExcludesFullRoutingFeatures() {
  const core = buildSource(false);
  for (const forbidden of ["hashchange", "beforeSend", "consent", "data.manual"]) {
    assert.equal(core.includes(forbidden), false, `core build contains ${forbidden}`);
  }
}

testIdentifyAndSubsequentEvents();
testClear();
testOmittedTraits();
testValidationBoundaries();
testManualPageviews();
testHashRouting();
testReplaceStateRouting();
testDuplicateNavigationIsDropped();
testBeforeSend();
testNamedBeforeSendIsResolvedAtSendTime();
testConsentGrantFlushesInOrderThroughBeforeSend();
testConsentQueueKeepsNewestTwentyEvents();
testConsentDenialClearsQueueAndDropsUntilGranted();
testConsentGrantIsIdempotent();
testConsentQueuesManualPageview();
testConsentIsNoOpWithoutGate();
testCoreBuildExcludesFullRoutingFeatures();
