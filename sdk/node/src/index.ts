import { randomUUID } from "node:crypto";

import {
  MAX_BODY_BYTES,
  MAX_VALUE_BYTES,
  byteLength,
  sanitizeProps,
  sanitizeRevenue,
  sanitizeTraits,
  validEventUrl,
  validName,
  validProfileId,
  type PropValue,
  type Props,
  type Revenue,
  type Traits,
} from "./validate.js";

export type { PropValue, Props, Revenue, Traits } from "./validate.js";
export { sanitizeProps, sanitizeRevenue, sanitizeTraits } from "./validate.js";

export const VERSION = "0.2.0";
export const DEFAULT_USER_AGENT = `supermetry-node/${VERSION}`;
export const EVENTS_PATH = "/api/public/v1/events";
/** Supermetry Cloud, used when `host` is omitted. */
export const DEFAULT_HOST = "https://app.supermetry.com";

/** The JSON body sent to `POST /api/public/v1/events`. */
export interface EventPayload {
  v: 1;
  id: string;
  ts: number;
  site: string;
  name: string;
  url: string;
  referrer?: string;
  screen_width?: number;
  props?: Record<string, PropValue>;
  revenue?: { amount: string | number; currency?: string };
  profile_id?: string;
  traits?: Record<string, string>;
}

export interface SupermetryOptions {
  /** Public site ID. */
  site: string;
  /** Supermetry origin. Defaults to Supermetry Cloud (`https://app.supermetry.com`); set it when self-hosting. */
  host?: string;
  /** Full events endpoint; defaults to `host + "/api/public/v1/events"`. */
  endpoint?: string;
  /** Default page URL for events that do not pass one. Relative event URLs resolve against it. */
  url?: string;
  /** User-Agent used when no visitor UA is forwarded. Defaults to `supermetry-node/0.2.0`. */
  userAgent?: string;
  /** Send nothing (every call resolves immediately). */
  disabled?: boolean;
  /** Called for validation, network and HTTP errors. Tracking never throws. */
  onError?: (error: SupermetryError) => void;
  /** Custom fetch implementation; defaults to the global `fetch`. */
  fetch?: typeof fetch;
  /** Per-attempt timeout in milliseconds (default 10000). */
  timeout?: number;
  /** Retry once on network errors, 429 and 5xx (default true). */
  retry?: boolean;
  /** Send an `Origin` header derived from the event URL (for sites with strict origin). */
  sendOrigin?: boolean;
}

/** Per-event request context. Server SDK calls are stateless: pass `profileId` on each call. */
export interface EventContext {
  /** Absolute page URL (or relative to the configured `url`). */
  url?: string;
  referrer?: string;
  /** Profile ID set by `identify`; attach it to every event of that user. */
  profileId?: string;
  /** Visitor User-Agent, forwarded as `User-Agent`. */
  userAgent?: string;
  /** Visitor IP, forwarded as `X-Forwarded-For`. */
  ip?: string;
  screenWidth?: number;
  /** Event time; defaults to now. */
  timestamp?: Date | number;
}

export interface TrackOptions extends EventContext {
  props?: Props;
  revenue?: Revenue;
}

export interface PageviewOptions extends EventContext {
  props?: Props;
}

export interface SendResult {
  ok: boolean;
  /** HTTP status of the last attempt, when a response was received. */
  status?: number;
  error?: SupermetryError;
}

export class SupermetryError extends Error {
  readonly status?: number;
  readonly event?: EventPayload;
  constructor(message: string, options: { status?: number; event?: EventPayload; cause?: unknown } = {}) {
    super(message, options.cause === undefined ? undefined : { cause: options.cause });
    this.name = "SupermetryError";
    this.status = options.status;
    this.event = options.event;
  }
}

export function newEventId(): string {
  return randomUUID().replace(/-/g, "");
}

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

export class Supermetry {
  readonly site: string;
  readonly endpoint: string;
  private readonly options: SupermetryOptions;
  private readonly inflight = new Set<Promise<unknown>>();
  private closed = false;

  constructor(options: SupermetryOptions) {
    if (!options || typeof options.site !== "string" || options.site === "") {
      throw new TypeError("@supermetry/node: `site` is required");
    }
    if (!options.endpoint && options.host !== undefined && (typeof options.host !== "string" || options.host === "")) {
      throw new TypeError("@supermetry/node: `host` must be a non-empty string");
    }
    this.options = options;
    this.site = options.site;
    this.endpoint = options.endpoint ?? (options.host ?? DEFAULT_HOST).replace(/\/+$/, "") + EVENTS_PATH;
  }

  /** Sends a custom event. Never rejects; await it to wait for delivery. */
  track(name: string, options: TrackOptions = {}): Promise<SendResult> {
    return this.capture(() => {
      if (name === "identify") throw new SupermetryError('use identify() instead of track("identify")');
      return this.build(name, options, { props: options.props, revenue: options.revenue });
    });
  }

  /** Sends a pageview for `url` (or the configured default URL). */
  pageview(options: PageviewOptions = {}): Promise<SendResult> {
    return this.capture(() => this.build("pageview", options, { props: options.props }));
  }

  /** Links the visitor to `profileId` and records traits (string → string, max 20). */
  identify(profileId: string, traits?: Traits, context: EventContext = {}): Promise<SendResult> {
    return this.capture(() => {
      if (!validProfileId(profileId)) throw new SupermetryError("identify: profileId must be 1-128 characters");
      return this.build("identify", { ...context, profileId }, { traits: sanitizeTraits(traits) });
    });
  }

  /** Resolves once every in-flight send has finished. */
  async flush(): Promise<void> {
    while (this.inflight.size > 0) await Promise.allSettled([...this.inflight]);
  }

  /** Flushes and stops accepting new events. */
  async close(): Promise<void> {
    this.closed = true;
    await this.flush();
  }

  private capture(build: () => { event: EventPayload; context: EventContext }): Promise<SendResult> {
    if (this.options.disabled || this.closed) return Promise.resolve({ ok: false });
    let built: { event: EventPayload; context: EventContext };
    try {
      built = build();
    } catch (error) {
      const err = error instanceof SupermetryError ? error : new SupermetryError(String(error), { cause: error });
      this.report(err);
      return Promise.resolve({ ok: false, error: err });
    }
    const promise = this.send(built.event, built.context);
    this.inflight.add(promise);
    promise.finally(() => this.inflight.delete(promise));
    return promise;
  }

  private build(
    name: string,
    context: EventContext,
    extra: { props?: Props; revenue?: Revenue; traits?: Record<string, string> },
  ): { event: EventPayload; context: EventContext } {
    if (!validName(name)) throw new SupermetryError("event name must be 1-300 bytes");
    const url = this.resolveUrl(context.url);
    if (!url) throw new SupermetryError("event url must be an absolute http(s) URL (pass `url` or configure a default)");
    const ts = context.timestamp instanceof Date ? context.timestamp.getTime()
      : typeof context.timestamp === "number" ? Math.trunc(context.timestamp) : Date.now();
    const event: EventPayload = { v: 1, id: newEventId(), ts, site: this.site, name, url };
    if (typeof context.referrer === "string" && context.referrer !== "" && byteLength(context.referrer) <= MAX_VALUE_BYTES) {
      event.referrer = context.referrer;
    }
    if (typeof context.screenWidth === "number" && Number.isInteger(context.screenWidth) &&
      context.screenWidth > 0 && context.screenWidth <= 32767) {
      event.screen_width = context.screenWidth;
    }
    const props = sanitizeProps(extra.props);
    if (props) event.props = props;
    if (name !== "pageview") {
      const revenue = sanitizeRevenue(extra.revenue);
      if (revenue) event.revenue = revenue;
    }
    if (context.profileId !== undefined && context.profileId !== null) {
      if (validProfileId(context.profileId)) event.profile_id = context.profileId;
      else this.report(new SupermetryError("profileId must be 1-128 characters; sent without it"));
    }
    // Traits are only allowed on identify; on any other event the server rejects the request.
    if (name === "identify" && extra.traits) event.traits = extra.traits;
    if (byteLength(JSON.stringify(event)) > MAX_BODY_BYTES) {
      throw new SupermetryError("event body exceeds 8 KB", { event });
    }
    return { event, context };
  }

  private resolveUrl(url: string | undefined): string | undefined {
    const base = this.options.url;
    if (url === undefined || url === "") return validEventUrl(base) ? base : undefined;
    if (validEventUrl(url)) return url;
    if (!base) return undefined;
    try {
      const resolved = new URL(url, base).toString();
      return validEventUrl(resolved) ? resolved : undefined;
    } catch {
      return undefined;
    }
  }

  private async send(event: EventPayload, context: EventContext): Promise<SendResult> {
    const doFetch = this.options.fetch ?? globalThis.fetch;
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      "User-Agent": context.userAgent || this.options.userAgent || DEFAULT_USER_AGENT,
    };
    if (context.ip) headers["X-Forwarded-For"] = context.ip;
    if (this.options.sendOrigin) headers["Origin"] = new URL(event.url).origin;
    const body = JSON.stringify(event);
    const attempts = this.options.retry === false ? 1 : 2;
    let last: SendResult = { ok: false };

    for (let attempt = 1; attempt <= attempts; attempt++) {
      let retryDelay = 250;
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(), this.options.timeout ?? 10_000);
      (timer as { unref?: () => void }).unref?.();
      try {
        const response = await doFetch(this.endpoint, { method: "POST", headers, body, signal: controller.signal });
        // Drain the body so keep-alive sockets are released.
        await response.arrayBuffer().catch(() => undefined);
        if (response.ok) return { ok: true, status: response.status };
        const error = new SupermetryError(`Supermetry responded ${response.status}`, { status: response.status, event });
        last = { ok: false, status: response.status, error };
        if (response.status !== 429 && response.status < 500) break;
        const retryAfter = Number(response.headers.get("retry-after"));
        if (Number.isFinite(retryAfter) && retryAfter > 0) retryDelay = Math.min(retryAfter * 1000, 5000);
      } catch (cause) {
        last = { ok: false, error: new SupermetryError("Supermetry request failed", { event, cause }) };
      } finally {
        clearTimeout(timer);
      }
      if (attempt < attempts) await sleep(retryDelay);
    }
    if (last.error) this.report(last.error);
    return last;
  }

  private report(error: SupermetryError): void {
    try {
      this.options.onError?.(error);
    } catch {
      // A throwing error hook must not break the host application.
    }
  }
}

export function createSupermetry(options: SupermetryOptions): Supermetry {
  return new Supermetry(options);
}
