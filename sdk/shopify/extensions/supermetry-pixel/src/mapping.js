// Maps Shopify customer events to Supermetry events (sdk/SPEC.md). Pure functions
// with no Shopify imports, shared by the web pixel extension (src/index.js) and the
// generated "no app" custom pixel (no-app/custom-pixel.js), and unit tested.

export const EVENTS_PATH = "/api/public/v1/events";

export const SUBSCRIBED_EVENTS = [
  "page_viewed",
  "product_viewed",
  "product_added_to_cart",
  "checkout_started",
  "checkout_completed",
];

export const EVENT_NAMES = {
  page_viewed: "pageview",
  product_viewed: "Product Viewed",
  product_added_to_cart: "Add to Cart",
  checkout_started: "Checkout Started",
  checkout_completed: "Purchase",
};

const PAGEVIEW_MODES = ["checkout", "all", "none"];

// Returns { host, site, pageviews } or null when the settings cannot track.
export function normalizeSettings(raw) {
  const site = String(raw?.site ?? "").trim().toLowerCase();
  let host = String(raw?.host ?? "").trim() || "https://app.supermetry.com";
  if (!/^[a-z0-9]{12}$/.test(site)) return null;
  try {
    const url = new URL(host);
    if ((url.protocol !== "https:" && url.protocol !== "http:") || url.username || url.password || url.search || url.hash) {
      return null;
    }
    host = url.origin + url.pathname.replace(/\/+$/, "");
  } catch {
    return null;
  }
  const mode = String(raw?.pageviews ?? "").trim().toLowerCase();
  return { host, site, pageviews: PAGEVIEW_MODES.includes(mode) ? mode : "checkout" };
}

// Checkout and order-status URLs carry access tokens in the path and query.
export function isCheckoutUrl(href) {
  try {
    return /\/(checkouts|orders)\//.test(new URL(href).pathname) || /\/thank[_-]you\b/.test(new URL(href).pathname);
  } catch {
    return false;
  }
}

// Drops the fragment everywhere, and the query string on checkout pages (UTM
// parameters on storefront pages are kept for attribution).
export function cleanUrl(href) {
  try {
    const url = new URL(href);
    url.hash = "";
    if (isCheckoutUrl(href)) url.search = "";
    return url.href;
  } catch {
    return "";
  }
}

function eventId(value) {
  const id = String(value ?? "").replace(/[^A-Za-z0-9_-]/g, "").slice(0, 64);
  return id || undefined;
}

// Shopify IDs may be GIDs ("gid://shopify/Order/123"); keep the trailing number.
function shortId(value) {
  if (value === undefined || value === null || value === "") return undefined;
  const text = String(value);
  return text.slice(text.lastIndexOf("/") + 1).slice(0, 100);
}

function money(value) {
  const amount = Number(value?.amount);
  return Number.isFinite(amount) && amount >= 0 ? String(Math.round(amount * 1e6) / 1e6) : undefined;
}

function currency(value) {
  const code = String(value?.currencyCode ?? "").toUpperCase();
  return /^[A-Z]{3}$/.test(code) ? code : undefined;
}

function compact(props) {
  const out = {};
  for (const [key, value] of Object.entries(props)) {
    if (value === undefined || value === null || value === "") continue;
    out[key] = typeof value === "string" ? value.slice(0, 500) : value;
  }
  return out;
}

function variantProps(variant) {
  return {
    product_id: shortId(variant?.product?.id),
    product: variant?.product?.title,
    variant_id: shortId(variant?.id),
    variant: variant?.title,
    sku: variant?.sku,
    price: money(variant?.price),
  };
}

function itemCount(checkout) {
  const lines = Array.isArray(checkout?.lineItems) ? checkout.lineItems : [];
  return lines.reduce((sum, line) => sum + (Number(line?.quantity) || 0), 0);
}

function eventProps(name, data) {
  switch (name) {
    case "product_viewed":
      return variantProps(data?.productVariant);
    case "product_added_to_cart": {
      const line = data?.cartLine;
      return { ...variantProps(line?.merchandise), quantity: Number(line?.quantity) || undefined, value: money(line?.cost?.totalAmount) };
    }
    case "checkout_started":
      return { items: itemCount(data?.checkout), value: money(data?.checkout?.totalPrice) };
    case "checkout_completed": {
      const checkout = data?.checkout;
      const codes = (Array.isArray(checkout?.discountApplications) ? checkout.discountApplications : [])
        .map((discount) => discount?.title)
        .filter(Boolean);
      return { order_id: shortId(checkout?.order?.id), items: itemCount(checkout), coupon: codes.join(",") };
    }
    default:
      return {};
  }
}

// Builds the Supermetry payload for a Shopify customer event, or null when the
// event is not sent.
export function toPayload(event, rawSettings) {
  const settings = normalizeSettings(rawSettings);
  const name = EVENT_NAMES[event?.name];
  if (!settings || !name) return null;
  const href = event?.context?.document?.location?.href ?? "";
  const url = cleanUrl(href);
  if (!url) return null;
  if (name === "pageview") {
    if (settings.pageviews === "none") return null;
    // The theme app embed (or theme snippet) already counts storefront pages.
    if (settings.pageviews === "checkout" && !isCheckoutUrl(href)) return null;
  }
  const timestamp = Date.parse(event?.timestamp ?? "");
  const payload = {
    v: 1,
    id: eventId(event?.id),
    ts: Number.isFinite(timestamp) ? timestamp : Date.now(),
    site: settings.site,
    name,
    url,
    referrer: cleanUrl(event?.context?.document?.referrer ?? ""),
    props: name === "pageview" ? {} : compact(eventProps(event.name, event.data)),
  };
  const width = Number(event?.context?.window?.screen?.width);
  if (Number.isInteger(width) && width > 0 && width <= 32767) payload.screen_width = width;
  if (event.name === "checkout_completed") {
    const checkout = event.data?.checkout;
    const amount = money(checkout?.totalPrice);
    const code = currency(checkout?.totalPrice) ?? currency(checkout);
    if (amount !== undefined) payload.revenue = code ? { amount, currency: code } : { amount };
    // A stable ID per order lets the server drop a duplicate (thank-you page reloads).
    const order = payload.props.order_id;
    if (order) payload.id = eventId(`shopify_order_${order}`);
  }
  if (!payload.id) delete payload.id;
  return payload;
}

// The fetch() arguments for a payload: a simple no-cors request with a text body,
// like the Supermetry tracker sends, so no CORS preflight is needed.
export function toRequest(event, rawSettings) {
  const payload = toPayload(event, rawSettings);
  if (!payload) return null;
  const settings = normalizeSettings(rawSettings);
  return {
    url: settings.host + EVENTS_PATH,
    init: { method: "POST", body: JSON.stringify(payload), keepalive: true, mode: "no-cors" },
  };
}
