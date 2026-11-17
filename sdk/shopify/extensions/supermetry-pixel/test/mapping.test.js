import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import vm from "node:vm";

import { cleanUrl, isCheckoutUrl, normalizeSettings, toPayload, toRequest } from "../src/mapping.js";

const settings = { host: "https://stats.example.com/", site: "abcdefabcdef", pageviews: "checkout" };
const context = (href, referrer = "") => ({ document: { location: { href }, referrer }, window: { screen: { width: 1440 } } });

const variant = {
  id: "gid://shopify/ProductVariant/42",
  title: "Blue / M",
  sku: "TEE-BL-M",
  price: { amount: 25, currencyCode: "EUR" },
  product: { id: "gid://shopify/Product/7", title: "Tee", vendor: "Acme" },
};

const completed = {
  id: "sh-88e0-4a6f-B1C2",
  name: "checkout_completed",
  timestamp: "2026-11-03T10:00:00.000Z",
  context: context("https://shop.example.com/checkouts/c/abc123/thank-you?key=secret#x"),
  data: {
    checkout: {
      currencyCode: "EUR",
      totalPrice: { amount: 49.9, currencyCode: "EUR" },
      order: { id: "gid://shopify/OrderIdentity/1001" },
      lineItems: [{ quantity: 2 }, { quantity: 1 }],
      discountApplications: [{ title: "WELCOME10" }],
    },
  },
};

test("normalizeSettings validates the site and host", () => {
  assert.deepEqual(normalizeSettings(settings), { host: "https://stats.example.com", site: "abcdefabcdef", pageviews: "checkout" });
  assert.equal(normalizeSettings({ site: "abcdefabcdef" }).host, "https://app.supermetry.com");
  assert.equal(normalizeSettings({ site: "abcdefabcdef", pageviews: "ALL" }).pageviews, "all");
  assert.equal(normalizeSettings({ site: "abcdefabcdef", pageviews: "bogus" }).pageviews, "checkout");
  assert.equal(normalizeSettings({ site: "YOUR_SITE_ID" }), null);
  assert.equal(normalizeSettings({ site: "abcdefabcdef", host: "ftp://x" }), null);
  assert.equal(normalizeSettings({ site: "abcdefabcdef", host: "https://u:p@x.com" }), null);
});

test("checkout URLs lose their query string, storefront URLs keep UTM", () => {
  assert.ok(isCheckoutUrl("https://shop.example.com/checkouts/c/abc/thank-you"));
  assert.ok(isCheckoutUrl("https://shop.example.com/12345/orders/abcdef"));
  assert.ok(!isCheckoutUrl("https://shop.example.com/products/tee"));
  assert.equal(cleanUrl("https://shop.example.com/checkouts/c/abc?token=1#h"), "https://shop.example.com/checkouts/c/abc");
  assert.equal(cleanUrl("https://shop.example.com/?utm_source=x#h"), "https://shop.example.com/?utm_source=x");
  assert.equal(cleanUrl("not a url"), "");
});

test("checkout_completed maps to Purchase with revenue and a stable order ID", () => {
  const payload = toPayload(completed, settings);
  assert.deepEqual(payload, {
    v: 1,
    id: "shopify_order_1001",
    ts: Date.parse("2026-11-03T10:00:00.000Z"),
    site: "abcdefabcdef",
    name: "Purchase",
    url: "https://shop.example.com/checkouts/c/abc123/thank-you",
    referrer: "",
    screen_width: 1440,
    props: { order_id: "1001", items: 3, coupon: "WELCOME10" },
    revenue: { amount: "49.9", currency: "EUR" },
  });
});

test("checkout_completed without an order keeps the Shopify event ID", () => {
  const event = structuredClone(completed);
  delete event.data.checkout.order;
  const payload = toPayload(event, settings);
  assert.equal(payload.id, "sh-88e0-4a6f-B1C2");
  assert.equal(payload.props.order_id, undefined);
  assert.deepEqual(payload.revenue, { amount: "49.9", currency: "EUR" });
});

test("product events carry product properties", () => {
  const viewed = toPayload({ id: "e1", name: "product_viewed", timestamp: "2026-11-03T10:00:00Z", context: context("https://shop.example.com/products/tee", "https://google.com/"), data: { productVariant: variant } }, settings);
  assert.equal(viewed.name, "Product Viewed");
  assert.equal(viewed.referrer, "https://google.com/");
  assert.deepEqual(viewed.props, { product_id: "7", product: "Tee", variant_id: "42", variant: "Blue / M", sku: "TEE-BL-M", price: "25" });
  assert.equal(viewed.revenue, undefined);

  const added = toPayload({ id: "e2", name: "product_added_to_cart", context: context("https://shop.example.com/products/tee"), data: { cartLine: { quantity: 2, cost: { totalAmount: { amount: 50, currencyCode: "EUR" } }, merchandise: variant } } }, settings);
  assert.equal(added.name, "Add to Cart");
  assert.equal(added.props.quantity, 2);
  assert.equal(added.props.value, "50");

  const started = toPayload({ id: "e3", name: "checkout_started", context: context("https://shop.example.com/checkouts/c/abc?x=1"), data: completed.data }, settings);
  assert.equal(started.name, "Checkout Started");
  assert.deepEqual(started.props, { items: 3, value: "49.9" });
  assert.equal(started.revenue, undefined);
});

test("page views follow the pageviews setting", () => {
  const storefront = { id: "p1", name: "page_viewed", context: context("https://shop.example.com/") };
  const checkout = { id: "p2", name: "page_viewed", context: context("https://shop.example.com/checkouts/c/abc") };
  assert.equal(toPayload(storefront, settings), null);
  assert.deepEqual(toPayload(checkout, settings).props, {});
  assert.equal(toPayload(storefront, { ...settings, pageviews: "all" }).name, "pageview");
  assert.equal(toPayload(checkout, { ...settings, pageviews: "none" }), null);
});

test("unknown events and bad settings are not sent", () => {
  assert.equal(toPayload({ name: "search_submitted", context: context("https://shop.example.com/") }, settings), null);
  assert.equal(toPayload(completed, { site: "" }), null);
});

test("toRequest builds a no-cors keepalive POST to the events endpoint", () => {
  const request = toRequest(completed, settings);
  assert.equal(request.url, "https://stats.example.com/api/public/v1/events");
  assert.deepEqual({ ...request.init, body: undefined }, { method: "POST", body: undefined, keepalive: true, mode: "no-cors" });
  assert.equal(JSON.parse(request.init.body).name, "Purchase");
  assert.ok(request.init.body.length < 8192);
});

test("the generated custom pixel is current and sends the same events", async () => {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
  const { render } = await import(path.join(root, "scripts/build-custom-pixel.mjs"));
  const source = await readFile(path.join(root, "no-app/custom-pixel.js"), "utf8");
  assert.equal(source, await render(), "run node scripts/build-custom-pixel.mjs");

  const handlers = new Map();
  const sent = [];
  const sandbox = {
    analytics: { subscribe: (name, handler) => handlers.set(name, handler) },
    fetch: (url, init) => {
      sent.push({ url, init });
      return Promise.resolve();
    },
    URL,
    Date,
  };
  vm.runInNewContext(source.replace('site: "YOUR_SITE_ID"', 'site: "abcdefabcdef"'), sandbox);
  assert.deepEqual([...handlers.keys()], ["page_viewed", "product_viewed", "product_added_to_cart", "checkout_started", "checkout_completed"]);
  handlers.get("checkout_completed")(completed);
  assert.equal(sent.length, 1);
  assert.equal(sent[0].url, "https://app.supermetry.com/api/public/v1/events");
  assert.deepEqual(JSON.parse(sent[0].init.body), toPayload(completed, { site: "abcdefabcdef" }));
});
