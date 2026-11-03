// Maps Ghost member webhooks to Supermetry events (see sdk/SPEC.md).

export const EVENTS_PATH = "/api/public/v1/events";
export const USER_AGENT = "Supermetry-Ghost-Relay/0.1.0";

// Stripe amounts are in the currency's minor unit.
const ZERO_DECIMAL = new Set(["BIF", "CLP", "DJF", "GNF", "JPY", "KMF", "KRW", "MGA", "PYG", "RWF", "UGX", "VND", "VUV", "XAF", "XOF", "XPF"]);
const THREE_DECIMAL = new Set(["BHD", "JOD", "KWD", "OMR", "TND"]);

export function normalizeConfig(config = {}) {
  const site = String(config.site ?? "").trim().toLowerCase();
  if (!/^[a-z0-9]{12}$/.test(site)) throw new Error("SUPERMETRY_SITE must be the 12-character site ID");
  const host = httpBase(config.host || "https://app.supermetry.com", "SUPERMETRY_HOST");
  const siteUrl = httpUrl(config.siteUrl, "GHOST_URL");
  return {
    site,
    host,
    endpoint: host + EVENTS_PATH,
    siteUrl,
    signupEvent: eventName(config.signupEvent, "Signup"),
    subscriptionEvent: eventName(config.subscriptionEvent, "Subscription"),
    profileId: config.profileId === "uuid" ? "uuid" : "none",
  };
}

// Returns the Supermetry events for one webhook body (possibly none).
export function toEvents(body, config, { now = Date.now() } = {}) {
  const member = body?.member;
  const current = member?.current;
  if (!current || typeof current !== "object" || !current.id) return [];
  const previous = member.previous && typeof member.previous === "object" ? member.previous : {};
  const event = typeof body.event === "string" ? body.event : Object.keys(previous).length ? "member.edited" : "member.added";

  const events = [];
  const base = {
    v: 1,
    ts: now,
    site: config.site,
    url: eventUrl(current, config.siteUrl),
  };
  if (config.profileId === "uuid" && typeof current.uuid === "string" && current.uuid) {
    base.profile_id = current.uuid.slice(0, 128);
  }

  if (event === "member.added") {
    events.push({
      ...base,
      id: eventId(`ghost_signup_${current.id}`),
      name: config.signupEvent,
      props: compact({ status: current.status, source: "ghost_webhook" }),
    });
  }

  const becamePaid =
    current.status === "paid" &&
    (event === "member.added" || (event === "member.edited" && "status" in previous && previous.status !== "paid"));
  if (becamePaid) {
    const subscription = latestSubscription(current.subscriptions);
    const price = subscription?.price ?? subscription?.plan;
    const revenue = subscription?.status === "active" ? toRevenue(price) : undefined;
    events.push({
      ...base,
      id: eventId(`ghost_sub_${subscription?.id ?? current.id}`),
      name: config.subscriptionEvent,
      props: compact({
        tier: subscription?.tier?.name ?? (Array.isArray(current.tiers) ? current.tiers[0]?.name : undefined),
        interval: price?.interval,
        trial: subscription?.status === "trialing" ? true : undefined,
        source: "ghost_webhook",
      }),
      ...(revenue ? { revenue } : {}),
    });
  }
  return events;
}

export function toRequest(payload, config) {
  return {
    url: config.endpoint,
    init: {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "User-Agent": USER_AGENT,
        Origin: new URL(config.siteUrl).origin,
      },
      body: JSON.stringify(payload),
    },
  };
}

export function toRevenue(price) {
  const currency = typeof price?.currency === "string" ? price.currency.toUpperCase() : "";
  const minor = Number(price?.amount);
  if (!/^[A-Z]{3}$/.test(currency) || !Number.isSafeInteger(minor) || minor <= 0) return undefined;
  const digits = ZERO_DECIMAL.has(currency) ? 0 : THREE_DECIMAL.has(currency) ? 3 : 2;
  const text = String(minor).padStart(digits + 1, "0");
  const amount = digits ? `${text.slice(0, -digits)}.${text.slice(-digits)}` : text;
  return { amount, currency };
}

function latestSubscription(subscriptions) {
  if (!Array.isArray(subscriptions)) return undefined;
  return subscriptions
    .filter((sub) => sub && (sub.status === "active" || sub.status === "trialing"))
    .sort((a, b) => (Date.parse(b.start_date) || 0) - (Date.parse(a.start_date) || 0))[0];
}

// Signup attribution (the page the member signed up on) when Ghost includes it
// and it is on the site; otherwise the site URL.
function eventUrl(current, siteUrl) {
  const candidate = current?.attribution?.url;
  if (typeof candidate === "string" && candidate) {
    try {
      const url = new URL(candidate, siteUrl);
      if (url.origin === new URL(siteUrl).origin) {
        url.search = "";
        url.hash = "";
        return url.href.slice(0, 2000);
      }
    } catch {}
  }
  return siteUrl;
}

function eventId(value) {
  return String(value).replace(/[^A-Za-z0-9_-]/g, "_").slice(0, 64);
}

function eventName(value, fallback) {
  const name = String(value ?? "").trim();
  return name && name !== "pageview" && name !== "identify" ? name.slice(0, 300) : fallback;
}

function compact(props) {
  const out = {};
  for (const [key, value] of Object.entries(props)) {
    if (value === undefined || value === null || value === "") continue;
    out[key] = typeof value === "string" ? value.slice(0, 2000) : value;
  }
  return out;
}

function httpUrl(value, label) {
  let url;
  try {
    url = new URL(String(value ?? ""));
  } catch {
    throw new Error(`${label} must be an absolute http(s) URL`);
  }
  if (url.protocol !== "https:" && url.protocol !== "http:") throw new Error(`${label} must be an absolute http(s) URL`);
  if (url.username || url.password) throw new Error(`${label} must not contain credentials`);
  url.search = "";
  url.hash = "";
  return url.href;
}

function httpBase(value, label) {
  return httpUrl(value, label).replace(/\/+$/, "");
}
