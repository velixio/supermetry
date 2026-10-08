const assert = require("node:assert/strict");
const fs = require("node:fs");
const vm = require("node:vm");

const source = fs.readFileSync(`${__dirname}/tracker.js`, "utf8");
const extensions = fs.readFileSync(`${__dirname}/tracker.extensions.js`, "utf8");

function buildSource(full) {
  return source.replace(/\/\*@full\*\/([\s\S]*?)\/\*@end\*\//g, full ? "$1" : "");
}

function loadTracker({full = false, dataset = {}, withExtensions = false} = {}) {
  const events = new Map();
  const listeners = new Map();
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
      const key = `document:${name}`;
      const registered = listeners.get(key) || [];
      registered.push(listener);
      listeners.set(key, registered);
      events.set(key, (...args) => registered.forEach((callback) => callback(...args)));
    },
    querySelector: () => null,
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
    URL,
    addEventListener(name, listener) {
      const key = `window:${name}`;
      const registered = listeners.get(key) || [];
      registered.push(listener);
      listeners.set(key, registered);
      events.set(key, (...args) => registered.forEach((callback) => callback(...args)));
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
  if (withExtensions) {
    vm.runInNewContext(extensions, context, {filename: "tracker.extensions.js"});
  }
  return {context, events, panel: window.panel, payloads};
}

function element({tagName = "BUTTON", classes = [], attributes = {}, parentElement = null} = {}) {
  const values = {...attributes};
  return {
    tagName,
    classList: classes,
    parentElement,
    id: values.id || "",
    attributes: Object.entries(values).map(([name, value]) => ({name, value})),
    closest(selector) {
      return selector == "a" && tagName == "A" ? this : null;
    },
    getAttribute(name) {
      return Object.hasOwn(values, name) ? values[name] : null;
    },
  };
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
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview"]);
  assert.equal(tracker.payloads[0].url, "https://example.com/a");
  assert.deepEqual(tracker.payloads[0].props, {k: "v"});
}

function testManualEngagementRequiresStartedView() {
  const tracker = loadTracker({full: true, dataset: {manual: "true"}});

  tracker.events.get("window:scroll")();
  tracker.context.document.hidden = true;
  tracker.events.get("document:visibilitychange")();
  tracker.events.get("window:pagehide")();
  assert.deepEqual(tracker.payloads, [], "manual activity before a pageview must be ignored");

  tracker.context.document.hidden = false;
  tracker.panel.pageview();
  tracker.events.get("window:scroll")();
  tracker.events.get("window:pagehide")();
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview", "engagement"]);
}

function testManualPageviewResolvesRelativeURLAndIgnoresInvalidURL() {
  const relative = loadTracker({full: true, dataset: {manual: "true"}});
  relative.panel.pageview({url: "/virtual"});
  assert.deepEqual(relative.payloads.map((payload) => payload.name), ["pageview"]);
  assert.equal(relative.payloads[0].url, "https://example.com/virtual");

  const invalid = loadTracker({full: true, dataset: {manual: "true"}});
  assert.doesNotThrow(() => invalid.panel.pageview({url: "http://"}));
  assert.deepEqual(invalid.payloads, []);
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

function testSameLocationDedupeIgnoresVirtualPageviewURL() {
  const tracker = loadTracker({full: true});
  tracker.panel.pageview({url: "/virtual"});
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), [
    "pageview", "engagement", "pageview",
  ]);

  tracker.context.history.replaceState({}, "", "/start");
  assert.equal(tracker.payloads.length, 3, "same-location replaceState must not navigate");

  tracker.context.history.replaceState({}, "", "/real");
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), [
    "pageview", "engagement", "pageview", "engagement", "pageview",
  ]);
  assert.equal(tracker.payloads.at(-1).url, "https://example.com/real");
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

function testConsentQueueOverflowKeepsPageview() {
  const tracker = loadTracker({full: true, dataset: {consent: "required"}});
  for (let index = 1; index <= 25; index++) {
    tracker.panel.track(`Event ${index}`);
  }

  tracker.panel.consent(true);

  assert.equal(tracker.payloads.length, 20);
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), [
    "pageview",
    ...Array.from({length: 19}, (_, index) => `Event ${index + 7}`),
  ]);
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
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview"]);
  assert.equal(tracker.payloads.at(-1).url, "https://example.com/consented");
}

function testConsentIsNoOpWithoutGate() {
  const tracker = loadTracker({full: true});
  tracker.panel.consent(false);
  tracker.panel.track("Still enabled");

  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview", "Still enabled"]);
}

function testFormSubmitContainsOnlySafeMetadata() {
  const tracker = loadTracker({full: true, withExtensions: true});
  const form = element({
    tagName: "FORM",
    attributes: {id: "signup", name: "newsletter", action: "/signup?x=1"},
  });
  form.action = element({tagName: "INPUT", attributes: {name: "action", value: "secret"}});
  form.id = element({tagName: "INPUT", attributes: {name: "id", value: "wrong-id"}});
  form.email = "person@example.com";

  tracker.events.get("document:submit")({target: form});

  const payload = tracker.payloads.at(-1);
  assert.equal(payload.name, "Form: Submit");
  assert.deepEqual(payload.props, {
    form_id: "signup",
    form_name: "newsletter",
    action: "/signup",
  });
  const encoded = JSON.stringify(payload);
  for (const forbidden of ["?x=1", "email", "person@example.com", "secret", "wrong-id"]) {
    assert.equal(encoded.includes(forbidden), false, `form payload contains ${forbidden}`);
  }
}

function testFormTrackingCanBeDisabled() {
  const tracker = loadTracker({
    full: true,
    withExtensions: true,
    dataset: {forms: "false"},
  });

  tracker.events.get("document:submit")({target: element({tagName: "FORM"})});

  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview"]);
}

function testClassTaggedFormReplacesAutomaticFormEvent() {
  const tracker = loadTracker({full: true, withExtensions: true});
  const form = element({
    tagName: "FORM",
    classes: ["panel-event-Newsletter+Signup"],
    attributes: {action: "/signup"},
  });

  tracker.events.get("document:submit")({target: form});

  assert.deepEqual(tracker.payloads.slice(1).map((payload) => payload.name), ["Newsletter Signup"]);
}

function testClassTaggedClickIncludesValidProperties() {
  const tracker = loadTracker({full: true, withExtensions: true});
  const button = element({
    classes: ["panel-event-Buy", "panel-event-plan=pro"],
  });

  tracker.events.get("document:click")({button: 0, target: button});

  assert.equal(tracker.payloads.at(-1).name, "Buy");
  assert.deepEqual(tracker.payloads.at(-1).props, {plan: "pro"});
}

function testMalformedClassTagsAreIgnored() {
  const tracker = loadTracker({full: true, withExtensions: true});
  tracker.events.get("document:click")({
    button: 0,
    target: element({classes: ["panel-event-", "panel-event-=x"]}),
  });
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview"]);

  const classes = [
    "panel-event-Limited",
    `panel-event-${"k".repeat(65)}=value`,
    `panel-event-long=${"v".repeat(257)}`,
    ...Array.from({length: 21}, (_, index) => `panel-event-p${index}=v${index}`),
  ];
  tracker.events.get("document:click")({button: 0, target: element({classes})});

  const props = tracker.payloads.at(-1).props;
  assert.equal(Object.keys(props).length, 20);
  assert.equal(props.p19, "v19");
  assert.equal(props.p20, undefined);
  assert.equal(props["k".repeat(65)], undefined);
  assert.equal(props.long, undefined);
}

function testDataAttributeTakesPrecedenceOverClassTag() {
  const tracker = loadTracker({full: true, withExtensions: true});
  const button = element({
    classes: ["panel-event-Class+Event"],
    attributes: {"data-panel-event": "Attribute Event"},
  });

  tracker.events.get("document:click")({button: 0, target: button});

  assert.deepEqual(tracker.payloads.slice(1).map((payload) => payload.name), ["Attribute Event"]);
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
testManualEngagementRequiresStartedView();
testManualPageviewResolvesRelativeURLAndIgnoresInvalidURL();
testHashRouting();
testReplaceStateRouting();
testDuplicateNavigationIsDropped();
testSameLocationDedupeIgnoresVirtualPageviewURL();
testBeforeSend();
testNamedBeforeSendIsResolvedAtSendTime();
testConsentGrantFlushesInOrderThroughBeforeSend();
testConsentQueueKeepsNewestTwentyEvents();
testConsentQueueOverflowKeepsPageview();
testConsentDenialClearsQueueAndDropsUntilGranted();
testConsentGrantIsIdempotent();
testConsentQueuesManualPageview();
testConsentIsNoOpWithoutGate();
testFormSubmitContainsOnlySafeMetadata();
testFormTrackingCanBeDisabled();
testClassTaggedFormReplacesAutomaticFormEvent();
testClassTaggedClickIncludesValidProperties();
testMalformedClassTagsAreIgnored();
testDataAttributeTakesPrecedenceOverClassTag();
testCoreBuildExcludesFullRoutingFeatures();
