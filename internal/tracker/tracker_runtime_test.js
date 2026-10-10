const assert = require("node:assert/strict");
const fs = require("node:fs");
const vm = require("node:vm");

const source = fs.readFileSync(`${__dirname}/tracker.js`, "utf8");
const extensions = fs.readFileSync(`${__dirname}/tracker.extensions.js`, "utf8");

function buildSource(full) {
  return source.replace(/\/\*@full\*\/([\s\S]*?)\/\*@end\*\//g, full ? "$1" : "");
}

function loadTracker({full = false, dataset = {}, withExtensions = false, prelude = "", includeSite = true, globalPrivacyControl = false, doNotTrack = "0", ignored = false, crypto = {}} = {}) {
  const events = new Map();
  const listeners = new Map();
  const payloads = [];
  let now = 1000;
  const forbidStorage = () => {
    throw new Error("tracker accessed browser storage");
  };
  const storage = new Map(ignored ? [["supermetry_ignore", "1"]] : []);
  const storageTarget = {
    getItem(key) {
      if (key != "supermetry_ignore") forbidStorage();
      return storage.get(key) ?? null;
    },
    setItem(key, value) {
      if (key != "supermetry_ignore" || value != "1") forbidStorage();
      storage.set(key, value);
    },
    removeItem(key) {
      if (key != "supermetry_ignore") forbidStorage();
      storage.delete(key);
    },
  };
  Object.defineProperty(storageTarget, "supermetry_ignore", {
    configurable: true,
    get: () => storage.get("supermetry_ignore"),
    // Real Storage coerces values to strings.
    set: (value) => value == "1" ? storage.set("supermetry_ignore", String(value)) : forbidStorage(),
  });
  const localStorage = new Proxy(storageTarget, {
    deleteProperty(target, key) {
      if (key != "supermetry_ignore") forbidStorage();
      storage.delete(key);
      return true;
    },
  });
  const location = {hostname: "example.com", href: "https://example.com/start"};
  const document = {
    body: {scrollHeight: 1000},
    currentScript: {
      dataset: {...(includeSite ? {site: "site-1"} : {}), ...dataset},
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
      events.set(key, (...args) => {
        if (args[0] && typeof args[0] == "object" && !args[0].type) args[0].type = name;
        registered.forEach((callback) => callback(...args));
      });
    },
    querySelector: () => null,
  };
  Object.defineProperty(document, "cookie", {get: forbidStorage, set: forbidStorage});
  const window = {};
  Object.defineProperties(window, {
    localStorage: {value: localStorage},
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
    crypto,
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
    innerWidth: 1000,
    localStorage,
    location,
    navigator: {
      globalPrivacyControl,
      doNotTrack,
      webdriver: false,
      sendBeacon(_endpoint, body) {
        payloads.push(JSON.parse(body));
        return true;
      },
    },
    screen: {width: 1440},
    scrollY: 0,
    window,
  };
  vm.runInNewContext(prelude + buildSource(full), context, {filename: "tracker.js"});
  if (withExtensions) {
    vm.runInNewContext(extensions, context, {filename: "tracker.extensions.js"});
  }
  return {context, events, supermetry: window.supermetry, payloads, storage, setNow: (value) => now = value};
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

  tracker.supermetry.identify("customer-42", {plan: "pro", region: "eu"});
  tracker.supermetry.track("Signup", {source: "hero"});
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

function testPayloadContractFields() {
  const uuids = [
    "123e4567-e89b-12d3-a456-426614174000",
    "987e6543-e21b-12d3-a456-426614174111",
  ];
  const tracker = loadTracker({crypto: {randomUUID: () => uuids.shift()}});
  tracker.supermetry.track("Signup");

  assert.deepEqual(tracker.payloads.map((payload) => payload.id), [
    "123e4567e89b12d3a456426614174000",
    "987e6543e21b12d3a456426614174111",
  ]);
  assert.deepEqual(tracker.payloads.map((payload) => payload.ts), [1200, 1300]);
  assert.deepEqual(tracker.payloads.map((payload) => payload.v), [1, 1]);
}

function testPayloadIDFallsBackToRandomHex() {
  const tracker = loadTracker({crypto: {}});
  assert.match(tracker.payloads[0].id, /^[a-f0-9]{1,64}$/);
}

function testConsentQueueKeepsCreationTimestamp() {
  const tracker = loadTracker({
    full: true,
    dataset: {consent: "required", manual: "true"},
    crypto: {randomUUID: () => "123e4567-e89b-12d3-a456-426614174000"},
  });
  tracker.supermetry.track("Queued");
  tracker.setNow(9000);
  tracker.supermetry.consent(true);

  assert.equal(tracker.payloads[0].ts, 1100);
}

function testClear() {
  const tracker = loadTracker();
  tracker.supermetry.identify("customer-42", {});
  const beforeClear = tracker.payloads.length;
  tracker.supermetry.identify(null);
  assert.equal(tracker.payloads.length, beforeClear, "clearing must not send an event");
  tracker.supermetry.track("Signed out");
  assert.equal(tracker.payloads.at(-1).profile_id, undefined);
}

function testOmittedTraits() {
  const tracker = loadTracker();
  tracker.supermetry.identify("customer-42");
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
    tracker.supermetry.identify(id, traits);
    assert.equal(tracker.payloads.length, valid ? 2 : 1, name);
    tracker.supermetry.track("After identify");
    assert.equal(tracker.payloads.at(-1).profile_id, valid ? id : undefined, name);
  }
}

function testManualPageviews() {
  const tracker = loadTracker({full: true, dataset: {manual: "true"}});
  assert.deepEqual(tracker.payloads, [], "manual mode must suppress the initial pageview");

  tracker.context.history.pushState({}, "", "/automatic");
  assert.deepEqual(tracker.payloads, [], "manual mode must suppress pushState pageviews");

  tracker.supermetry.pageview({url: "https://example.com/a", props: {k: "v"}});
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
  tracker.supermetry.pageview();
  tracker.events.get("window:scroll")();
  tracker.events.get("window:pagehide")();
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview", "engagement"]);
}

function testManualPageviewResolvesRelativeURLAndIgnoresInvalidURL() {
  const relative = loadTracker({full: true, dataset: {manual: "true"}});
  relative.supermetry.pageview({url: "/virtual"});
  assert.deepEqual(relative.payloads.map((payload) => payload.name), ["pageview"]);
  assert.equal(relative.payloads[0].url, "https://example.com/virtual");

  const invalid = loadTracker({full: true, dataset: {manual: "true"}});
  assert.doesNotThrow(() => invalid.supermetry.pageview({url: "http://"}));
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
  tracker.supermetry.pageview({url: "/virtual"});
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
  edited.supermetry.beforeSend = (event) => ({...event, props: {x: "1"}, site: "other-site"});
  edited.supermetry.track("Edited", {ignored: "yes"});
  assert.deepEqual(edited.payloads.at(-1).props, {x: "1"});
  assert.equal(edited.payloads.at(-1).site, "site-1");

  for (const [name, hook] of [
    ["null", () => null],
    ["throw", () => { throw new Error("redaction failed"); }],
    ["string", () => "yes"],
    ["boolean", () => true],
  ]) {
    const tracker = loadTracker({full: true});
    tracker.supermetry.beforeSend = hook;
    const before = tracker.payloads.length;
    tracker.supermetry.track("Dropped");
    assert.equal(tracker.payloads.length, before, `${name} return must fail closed`);
  }
}

function testNamedBeforeSendIsResolvedAtSendTime() {
  const tracker = loadTracker({full: true, dataset: {beforeSend: "hook"}});
  tracker.context.window.hook = (event) => ({...event, props: {version: "first"}});
  tracker.supermetry.track("First");
  tracker.context.window.hook = (event) => ({...event, props: {version: "second"}});
  tracker.supermetry.track("Second");
  assert.deepEqual(tracker.payloads.slice(-2).map((payload) => payload.props), [
    {version: "first"}, {version: "second"},
  ]);
}

function testConsentGrantFlushesInOrderThroughBeforeSend() {
  const tracker = loadTracker({full: true, dataset: {consent: "required"}});
  assert.equal(typeof tracker.supermetry.consent, "function");
  tracker.supermetry.beforeSend = (event) => ({...event, props: {approved: event.name}});
  tracker.supermetry.track("A");

  assert.deepEqual(tracker.payloads, [], "consent mode must start paused");
  tracker.supermetry.consent(true);

  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview", "A"]);
  assert.deepEqual(tracker.payloads.map((payload) => payload.props.approved), ["pageview", "A"]);
}

function testConsentQueueKeepsNewestTwentyEvents() {
  const tracker = loadTracker({full: true, dataset: {consent: "required", manual: "true"}});
  for (let index = 1; index <= 25; index++) {
    tracker.supermetry.track(`Event ${index}`);
  }

  tracker.supermetry.consent(true);

  assert.deepEqual(tracker.payloads.map((payload) => payload.name),
    Array.from({length: 20}, (_, index) => `Event ${index + 6}`));
}

function testConsentQueueOverflowKeepsPageview() {
  const tracker = loadTracker({full: true, dataset: {consent: "required"}});
  for (let index = 1; index <= 25; index++) {
    tracker.supermetry.track(`Event ${index}`);
  }

  tracker.supermetry.consent(true);

  assert.equal(tracker.payloads.length, 20);
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), [
    "pageview",
    ...Array.from({length: 19}, (_, index) => `Event ${index + 7}`),
  ]);
}

function testConsentDenialClearsQueueAndDropsUntilGranted() {
  const tracker = loadTracker({full: true, dataset: {consent: "required", manual: "true"}});
  tracker.supermetry.track("Before denial");
  tracker.supermetry.consent(false);
  tracker.supermetry.track("While denied");
  tracker.supermetry.consent(true);

  assert.deepEqual(tracker.payloads, []);
  tracker.supermetry.track("After grant");
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["After grant"]);
}

function testConsentGrantIsIdempotent() {
  const tracker = loadTracker({full: true, dataset: {consent: "required"}});
  tracker.supermetry.consent(true);
  tracker.supermetry.consent(true);

  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview"]);
}

function testConsentQueuesManualPageview() {
  const tracker = loadTracker({full: true, dataset: {consent: "required", manual: "true"}});
  tracker.supermetry.pageview({url: "https://example.com/consented"});
  assert.deepEqual(tracker.payloads, []);

  tracker.supermetry.consent(true);
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview"]);
  assert.equal(tracker.payloads.at(-1).url, "https://example.com/consented");
}

function testConsentWithdrawalWithoutGate() {
  const tracker = loadTracker({full: true});
  tracker.supermetry.consent(false);
  tracker.supermetry.track("Still enabled");

  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview"]);
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
    classes: ["supermetry-event-Newsletter+Signup"],
    attributes: {action: "/signup"},
  });

  tracker.events.get("document:submit")({target: form});

  assert.deepEqual(tracker.payloads.slice(1).map((payload) => payload.name), ["Newsletter Signup"]);
}

function testClassTaggedClickIncludesValidProperties() {
  const tracker = loadTracker({full: true, withExtensions: true});
  const button = element({
    classes: ["supermetry-event-Buy", "supermetry-event-plan=pro"],
  });

  tracker.events.get("document:click")({button: 0, target: button});

  assert.equal(tracker.payloads.at(-1).name, "Buy");
  assert.deepEqual(tracker.payloads.at(-1).props, {plan: "pro"});
}

function testMalformedClassTagsAreIgnored() {
  const tracker = loadTracker({full: true, withExtensions: true});
  tracker.events.get("document:click")({
    button: 0,
    target: element({classes: ["supermetry-event-", "supermetry-event-=x"]}),
  });
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview"]);

  const classes = [
    "supermetry-event-Limited",
    `supermetry-event-${"k".repeat(65)}=value`,
    `supermetry-event-long=${"v".repeat(257)}`,
    ...Array.from({length: 21}, (_, index) => `supermetry-event-p${index}=v${index}`),
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
    classes: ["supermetry-event-Class+Event"],
    attributes: {"data-supermetry-event": "Attribute Event"},
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

function testOptOutStopsSendsAndOptInResumes() {
  const tracker = loadTracker();
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview"]);

  tracker.supermetry.optOut();
  assert.equal(tracker.storage.get("supermetry_ignore"), "1");
  tracker.supermetry.track("Ignored");
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview"]);

  tracker.supermetry.optIn();
  assert.equal(tracker.storage.has("supermetry_ignore"), false);
  tracker.supermetry.track("Resumed");
  assert.deepEqual(tracker.payloads.map((payload) => payload.name), ["pageview", "Resumed"]);
}

function testStoredOptOutSuppressesInitialPageview() {
  const tracker = loadTracker({ignored: true});
  assert.deepEqual(tracker.payloads, []);
}

function testHeatmapIsOffByDefaultAndExplicitlyEnabled() {
  const off = loadTracker({full: true, withExtensions: true});
  off.events.get("document:click")({button: 0, pageX: 20, pageY: 30, target: element()});
  off.events.get("window:pagehide")();
  assert.equal(off.payloads.some((payload) => payload.name == "heatmap"), false);

  const on = loadTracker({full: true, withExtensions: true, dataset: {heatmap: "true"}});
  on.events.get("document:click")({button: 0, pageX: 20, pageY: 30, target: element()});
  on.events.get("window:pagehide")();
  assert.equal(on.payloads.some((payload) => payload.name == "heatmap"), true);

  const generated = loadTracker({
    full: true,
    withExtensions: true,
    prelude: `(d=>{d.heatmap??="true";})(document.currentScript.dataset);\n`,
  });
  generated.events.get("document:click")({button: 0, pageX: 20, pageY: 30, target: element()});
  generated.events.get("window:pagehide")();
  assert.equal(generated.payloads.some((payload) => payload.name == "heatmap"), true);
}

function testGeneratedPreludeAppliesDefaultsWithoutOverridingAttributes() {
  const defaults = loadTracker({
    full: true,
    withExtensions: true,
    dataset: {},
    includeSite: false,
    prelude: `(d=>{d.site??="abc123def456";d.outbound??="false";})(document.currentScript.dataset);\n`,
  });
  assert.equal(defaults.context.document.currentScript.dataset.site, "abc123def456");
  assert.equal(defaults.context.document.currentScript.dataset.outbound, "false");

  const attributeWins = loadTracker({
    full: true,
    withExtensions: true,
    dataset: {outbound: "true"},
    prelude: `(d=>{d.outbound??="false";})(document.currentScript.dataset);\n`,
  });
  assert.equal(attributeWins.context.document.currentScript.dataset.outbound, "true");
}

testIdentifyAndSubsequentEvents();
testPayloadContractFields();
testPayloadIDFallsBackToRandomHex();
testConsentQueueKeepsCreationTimestamp();
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
testConsentWithdrawalWithoutGate();
testFormSubmitContainsOnlySafeMetadata();
testFormTrackingCanBeDisabled();
testClassTaggedFormReplacesAutomaticFormEvent();
testClassTaggedClickIncludesValidProperties();
testMalformedClassTagsAreIgnored();
testDataAttributeTakesPrecedenceOverClassTag();
testCoreBuildExcludesFullRoutingFeatures();
testOptOutStopsSendsAndOptInResumes();
testStoredOptOutSuppressesInitialPageview();
testHeatmapIsOffByDefaultAndExplicitlyEnabled();
testGeneratedPreludeAppliesDefaultsWithoutOverridingAttributes();

function testPrivacySignalsBlockAllCollection() {
 for(const signal of [{globalPrivacyControl:true},{doNotTrack:"1"}]) {
  const tracker=loadTracker({...signal,full:true,withExtensions:true,dataset:{heatmap:"true"}});
  tracker.supermetry.optIn();tracker.supermetry.consent(true);tracker.supermetry.identify("visitor");tracker.supermetry.track("custom");
  tracker.events.get("document:click")({button:0,pageX:12,pageY:34,target:element()});tracker.events.get("window:pagehide")();
  assert.deepEqual(tracker.payloads,[],"privacy signal must veto every collection path");
 }
}
function testHeatmapConsentWithdrawalDiscardsPoints() {
 const tracker=loadTracker({full:true,withExtensions:true,dataset:{heatmap:"true",consent:"required"}});
 const click=()=>tracker.events.get("document:click")({button:0,pageX:12,pageY:34,target:element()});
 click();tracker.events.get("window:pagehide")();
 assert.deepEqual(tracker.payloads,[],"heatmap must respect required consent");
 tracker.supermetry.consent(true);click();tracker.supermetry.consent(false);tracker.supermetry.consent(true);
 tracker.events.get("window:pagehide")();
 assert.equal(tracker.payloads.some(p=>p.name==="heatmap"),false,"withdrawal must discard clicks even after regrant");
 click();tracker.events.get("window:pagehide")();
 assert.equal(tracker.payloads.filter(p=>p.name==="heatmap").length,1);
}
function testDisabledProfilesDoNotCollectIdentity() {
 const tracker=loadTracker({dataset:{profiles:"false"}});
 tracker.supermetry.identify("visitor",{email:"private"});tracker.supermetry.track("custom");
 assert.equal(tracker.payloads.some(p=>p.profile_id||p.traits||p.name==="identify"),false);
}
testPrivacySignalsBlockAllCollection();
testHeatmapConsentWithdrawalDiscardsPoints();
testDisabledProfilesDoNotCollectIdentity();
