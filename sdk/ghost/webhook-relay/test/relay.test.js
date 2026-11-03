import assert from "node:assert/strict";
import { createHmac } from "node:crypto";
import { once } from "node:events";
import { test } from "node:test";
import { createRelay, createRelayServer } from "../src/server.js";
import { normalizeConfig, toEvents, toRequest, toRevenue, USER_AGENT } from "../src/mapping.js";
import { parseSignature, sign, verifySignature } from "../src/signature.js";

const SECRET = "whsec_test";
const NOW = 1_750_000_000_000;
const OPTIONS = { site: "abcdef123456", siteUrl: "https://blog.example.com/", host: "https://sm.example.com/" };
const config = normalizeConfig(OPTIONS);

function member(overrides = {}) {
  return {
    id: "6650f0c1e4b0a1b2c3d4e5f6",
    uuid: "0b0a3c4e-1111-4222-8333-944455556666",
    email: "reader@example.com",
    name: "Reader",
    status: "free",
    subscriptions: [],
    ...overrides,
  };
}

const paidSubscription = {
  id: "sub_1PabcDEF",
  status: "active",
  start_date: "2025-06-01T10:00:00.000Z",
  tier: { name: "Supporter" },
  price: { amount: 500, currency: "usd", interval: "month" },
};

test("signature matches Ghost's scheme: HMAC-SHA256(secret, body + t) as hex", () => {
  const body = JSON.stringify({ member: { current: member(), previous: {} } });
  const hex = createHmac("sha256", SECRET).update(`${body}${NOW}`).digest("hex");
  const header = `sha256=${hex}, t=${NOW}`;
  assert.equal(sign(body, SECRET, NOW), header);
  assert.deepEqual(parseSignature(header), { signature: hex, timestamp: NOW });
  assert.equal(verifySignature(Buffer.from(body), header, SECRET, { now: NOW + 1000 }), true);
});

test("signature rejects tampering, wrong secret, stale timestamps and junk", () => {
  const body = '{"member":{}}';
  const header = sign(body, SECRET, NOW);
  assert.equal(verifySignature('{"member":{"x":1}}', header, SECRET, { now: NOW }), false);
  assert.equal(verifySignature(body, header, "other", { now: NOW }), false);
  assert.equal(verifySignature(body, header, SECRET, { now: NOW + 10 * 60 * 1000 }), false);
  assert.equal(verifySignature(body, header, SECRET, { now: NOW + 10 * 60 * 1000, toleranceMs: 0 }), true);
  assert.equal(verifySignature(body, header.replace(/t=\d+/, `t=${NOW + 1}`), SECRET, { now: NOW }), false);
  assert.equal(verifySignature(body, undefined, SECRET, { now: NOW }), false);
  assert.equal(verifySignature(body, "sha256=zz, t=1", SECRET, { now: NOW }), false);
  assert.equal(verifySignature(body, header, "", { now: NOW }), false);
});

test("config validation", () => {
  assert.equal(config.endpoint, "https://sm.example.com/api/public/v1/events");
  assert.equal(config.siteUrl, "https://blog.example.com/");
  assert.throws(() => normalizeConfig({ ...OPTIONS, site: "nope" }), /SUPERMETRY_SITE/);
  assert.throws(() => normalizeConfig({ ...OPTIONS, siteUrl: "ftp://x" }), /GHOST_URL/);
  assert.throws(() => normalizeConfig({ ...OPTIONS, host: "not a url" }), /SUPERMETRY_HOST/);
  assert.equal(normalizeConfig({ ...OPTIONS, signupEvent: "pageview" }).signupEvent, "Signup");
});

test("member.added (free) maps to a Signup with a stable id and no personal data", () => {
  const events = toEvents({ event: "member.added", member: { current: member(), previous: {} } }, config, { now: NOW });
  assert.deepEqual(events, [
    {
      v: 1,
      ts: NOW,
      site: "abcdef123456",
      url: "https://blog.example.com/",
      id: "ghost_signup_6650f0c1e4b0a1b2c3d4e5f6",
      name: "Signup",
      props: { status: "free", source: "ghost_webhook" },
    },
  ]);
  assert.doesNotMatch(JSON.stringify(events), /reader@example\.com|Reader/);
});

test("member.added without the event field is inferred from an empty previous", () => {
  const events = toEvents({ member: { current: member(), previous: {} } }, config, { now: NOW });
  assert.equal(events[0].name, "Signup");
});

test("member.added as paid adds a Subscription with revenue in major units", () => {
  const current = member({ status: "paid", subscriptions: [paidSubscription] });
  const events = toEvents({ event: "member.added", member: { current, previous: {} } }, config, { now: NOW });
  assert.deepEqual(events.map((e) => e.name), ["Signup", "Subscription"]);
  assert.deepEqual(events[1].revenue, { amount: "5.00", currency: "USD" });
  assert.equal(events[1].id, "ghost_sub_sub_1PabcDEF");
  assert.deepEqual(events[1].props, { tier: "Supporter", interval: "month", source: "ghost_webhook" });
});

test("member.edited free → paid maps to a Subscription; other edits are ignored", () => {
  const current = member({ status: "paid", subscriptions: [paidSubscription] });
  const upgraded = toEvents({ event: "member.edited", member: { current, previous: { status: "free" } } }, config, { now: NOW });
  assert.deepEqual(upgraded.map((e) => e.name), ["Subscription"]);

  assert.deepEqual(toEvents({ event: "member.edited", member: { current, previous: { name: "Old" } } }, config), []);
  assert.deepEqual(toEvents({ event: "member.edited", member: { current: member({ status: "free" }), previous: { status: "paid" } } }, config), []);
  assert.deepEqual(toEvents({ event: "member.deleted", member: { current: {}, previous: member() } }, config), []);
  assert.deepEqual(toEvents({ event: "post.published", post: { current: { id: "1" } } }, config), []);
  assert.deepEqual(toEvents(null, config), []);
});

test("trialing subscriptions carry no revenue; comped members are not subscriptions", () => {
  const trial = { ...paidSubscription, status: "trialing" };
  const [event] = toEvents({ event: "member.edited", member: { current: member({ status: "paid", subscriptions: [trial] }), previous: { status: "free" } } }, config);
  assert.equal(event.revenue, undefined);
  assert.equal(event.props.trial, true);
  assert.deepEqual(toEvents({ event: "member.edited", member: { current: member({ status: "comped" }), previous: { status: "free" } } }, config), []);
});

test("revenue respects currency minor units", () => {
  assert.deepEqual(toRevenue({ amount: 1999, currency: "eur" }), { amount: "19.99", currency: "EUR" });
  assert.deepEqual(toRevenue({ amount: 5, currency: "usd" }), { amount: "0.05", currency: "USD" });
  assert.deepEqual(toRevenue({ amount: 500, currency: "jpy" }), { amount: "500", currency: "JPY" });
  assert.deepEqual(toRevenue({ amount: 12345, currency: "kwd" }), { amount: "12.345", currency: "KWD" });
  assert.equal(toRevenue({ amount: 0, currency: "usd" }), undefined);
  assert.equal(toRevenue({ amount: "x", currency: "usd" }), undefined);
  assert.equal(toRevenue(undefined), undefined);
});

test("attribution URL is used only when it is on the Ghost site; profile id is opt-in", () => {
  const onSite = member({ attribution: { url: "/welcome/?ref=x#top" } });
  const offSite = member({ attribution: { url: "https://evil.example/" } });
  assert.equal(toEvents({ event: "member.added", member: { current: onSite, previous: {} } }, config)[0].url, "https://blog.example.com/welcome/");
  assert.equal(toEvents({ event: "member.added", member: { current: offSite, previous: {} } }, config)[0].url, "https://blog.example.com/");
  assert.equal(toEvents({ event: "member.added", member: { current: member(), previous: {} } }, config)[0].profile_id, undefined);
  const withProfile = normalizeConfig({ ...OPTIONS, profileId: "uuid" });
  assert.equal(toEvents({ event: "member.added", member: { current: member(), previous: {} } }, withProfile)[0].profile_id, member().uuid);
});

test("requests carry the relay UA (not a generic client UA) and the site Origin", () => {
  const { url, init } = toRequest({ v: 1, name: "Signup" }, config);
  assert.equal(url, "https://sm.example.com/api/public/v1/events");
  assert.equal(init.method, "POST");
  assert.equal(init.headers["User-Agent"], USER_AGENT);
  assert.equal(init.headers.Origin, "https://blog.example.com");
  assert.equal(init.headers["Content-Type"], "application/json");
});

async function startRelay(fetchImpl) {
  const server = createRelayServer({ ...OPTIONS, secret: SECRET, fetch: fetchImpl, log: { warn() {}, error() {} } });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const base = `http://127.0.0.1:${server.address().port}`;
  return { base, close: () => new Promise((resolve) => server.close(resolve)) };
}

test("relay server: verifies, relays, and asks Ghost to retry on upstream failure", async () => {
  const calls = [];
  let status = 202;
  const relay = await startRelay(async (url, init) => {
    calls.push({ url, init });
    return new Response(null, { status });
  });
  try {
    const body = JSON.stringify({ event: "member.added", member: { current: member(), previous: {} } });
    const post = (headers, payload = body) => fetch(`${relay.base}/ghost`, { method: "POST", headers: { "Content-Type": "application/json", ...headers }, body: payload });

    assert.equal((await post({})).status, 401);
    assert.equal((await post({ "X-Ghost-Signature": sign(body, "wrong", Date.now()) })).status, 401);
    assert.equal(calls.length, 0);

    const ok = await post({ "X-Ghost-Signature": sign(body, SECRET, Date.now()) });
    assert.equal(ok.status, 202);
    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, "https://sm.example.com/api/public/v1/events");
    assert.equal(JSON.parse(calls[0].init.body).name, "Signup");

    status = 503;
    assert.equal((await post({ "X-Ghost-Signature": sign(body, SECRET, Date.now()) })).status, 502);

    const bad = "not json";
    assert.equal((await post({ "X-Ghost-Signature": sign(bad, SECRET, Date.now()) }, bad)).status, 400);
    assert.equal((await fetch(`${relay.base}/ghost`)).status, 405);
    assert.equal((await fetch(`${relay.base}/other`, { method: "POST" })).status, 404);
    assert.equal((await fetch(`${relay.base}/healthz`)).status, 200);
  } finally {
    await relay.close();
  }
});

test("relay requires a secret", () => {
  assert.throws(() => createRelay({ ...OPTIONS }), /GHOST_WEBHOOK_SECRET/);
});
