import { AppState, type AppStateStatus } from "react-native";

import { VERSION, buildUserAgent, screenPath, screenWidth } from "./platform";
import {
  MAX_BODY_BYTES,
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
} from "./validate";

export const EVENTS_PATH = "/api/public/v1/events";
/** Supermetry Cloud, used when `host` is omitted. */
export const DEFAULT_HOST = "https://app.supermetry.com";

/** Async key/value storage; `@react-native-async-storage/async-storage` fits as-is. */
export interface SupermetryStorage {
  getItem(key: string): Promise<string | null>;
  setItem(key: string, value: string): Promise<void>;
  removeItem(key: string): Promise<void>;
}

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

export interface SupermetryReactNativeOptions {
  /** Public site ID. */
  site: string;
  /** Supermetry origin. Defaults to Supermetry Cloud (`https://app.supermetry.com`); set it when self-hosting. */
  host?: string;
  /** Base URL for screens, e.g. `https://app.supermetry.com`. Its host must be an allowed host of the site. */
  appUrl: string;
  /** Full events endpoint; defaults to `host + "/api/public/v1/events"`. */
  endpoint?: string;
  /** Persists profile ID, opt-out and undelivered events. Without it, state is memory-only. */
  storage?: SupermetryStorage;
  /** Send nothing. */
  disabled?: boolean;
  /** Called for validation, network and HTTP errors. Tracking never throws. */
  onError?: (error: Error) => void;
  /** Custom fetch implementation; defaults to the global `fetch`. */
  fetch?: typeof fetch;
  /** Override the generated browser-shaped User-Agent. */
  userAgent?: string;
  /** Max events kept in memory; the oldest are dropped first (default 500). */
  maxQueueSize?: number;
  /** Attempts per event before it is dropped (default 10). */
  maxAttempts?: number;
  /** First retry delay in ms, doubled per attempt up to `maxRetryDelay` (default 1000). */
  retryDelay?: number;
  /** Retry delay cap in ms (default 60000). */
  maxRetryDelay?: number;
  /** Storage key prefix (default `"supermetry:"`). */
  storagePrefix?: string;
}

export interface TrackOptions {
  revenue?: Revenue;
  /** Override the event URL (absolute, or a path under `appUrl`). Defaults to the current screen. */
  url?: string;
}

export interface SupermetryClient {
  /** Sends a pageview for `appUrl + "/" + slug(name)` and makes it the current screen. */
  screen(name: string, props?: Props): void;
  /** Sends a pageview for an absolute URL or a path under `appUrl`. */
  pageview(urlOrPath?: string, props?: Props): void;
  track(name: string, props?: Props, options?: TrackOptions): void;
  /** Links this device to `profileId` (persisted). `identify(null)` is `reset()`. */
  identify(profileId: string | null, traits?: Traits): void;
  /** Forgets the profile ID. */
  reset(): void;
  optOut(): Promise<void>;
  optIn(): Promise<void>;
  isOptedOut(): boolean;
  readonly profileId: string | undefined;
  /** Resolves once persisted state has loaded from storage. */
  readonly ready: Promise<void>;
  /** Tries to deliver every queued event now. */
  flush(): Promise<void>;
  /** Stops listening to AppState and cancels pending retries. */
  destroy(): void;
}

interface QueueItem {
  event: EventPayload;
  attempts: number;
  /** Enqueued before storage loaded; receives the persisted profile ID once known. */
  early?: boolean;
}

const MAX_AGE_MS = 24 * 60 * 60 * 1000 - 60_000;

function newEventId(): string {
  const bytes = new Uint8Array(16);
  const cryptoApi = (globalThis as { crypto?: { getRandomValues?: (a: Uint8Array) => Uint8Array } }).crypto;
  if (cryptoApi?.getRandomValues) cryptoApi.getRandomValues(bytes);
  else for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256);
  bytes[6] = (bytes[6]! & 0x0f) | 0x40;
  bytes[8] = (bytes[8]! & 0x3f) | 0x80;
  let out = "";
  for (const b of bytes) out += b.toString(16).padStart(2, "0");
  return out;
}

function toError(error: unknown): Error {
  return error instanceof Error ? error : new Error(String(error));
}

export class SupermetryHttpError extends Error {
  readonly status: number;
  constructor(status: number) {
    super(`Supermetry responded ${status}`);
    this.name = "SupermetryHttpError";
    this.status = status;
  }
}

class Client implements SupermetryClient {
  readonly ready: Promise<void>;
  private readonly options: SupermetryReactNativeOptions;
  private readonly endpoint: string;
  private readonly appUrl: string;
  private readonly userAgent: string;
  private readonly keys: { profile: string; optOut: string; queue: string };
  private queue: QueueItem[] = [];
  private currentProfile: string | undefined;
  private optedOut = false;
  private loaded = false;
  private profileTouched = false;
  private optOutTouched = false;
  private currentUrl: string | undefined;
  private flushing: Promise<void> | undefined;
  private retryTimer: ReturnType<typeof setTimeout> | undefined;
  private flushTimer: ReturnType<typeof setTimeout> | undefined;
  private appStateSubscription: { remove(): void } | undefined;
  private destroyed = false;

  constructor(options: SupermetryReactNativeOptions) {
    if (!options?.site) throw new TypeError("@supermetry/react-native: `site` is required");
    if (!validEventUrl(options.appUrl)) {
      throw new TypeError("@supermetry/react-native: `appUrl` must be an absolute http(s) URL");
    }
    this.options = options;
    this.endpoint = options.endpoint ?? (options.host || DEFAULT_HOST).replace(/\/+$/, "") + EVENTS_PATH;
    this.appUrl = options.appUrl.replace(/\/+$/, "");
    this.userAgent = options.userAgent ?? buildUserAgent();
    const prefix = options.storagePrefix ?? "supermetry:";
    this.keys = { profile: `${prefix}profile_id`, optOut: `${prefix}opt_out`, queue: `${prefix}queue` };
    this.ready = this.load();
    try {
      this.appStateSubscription = AppState.addEventListener("change", (state) => this.onAppState(state));
    } catch (error) {
      this.report(error);
    }
  }

  get profileId(): string | undefined {
    return this.currentProfile;
  }

  isOptedOut(): boolean {
    return this.optedOut;
  }

  screen(name: string, props?: Props): void {
    const path = screenPath(name);
    const url = `${this.appUrl}/${path}`;
    this.currentUrl = url;
    this.capture("pageview", url, { props });
  }

  pageview(urlOrPath?: string, props?: Props): void {
    const url = this.resolveUrl(urlOrPath);
    if (url) this.currentUrl = url;
    this.capture("pageview", url, { props });
  }

  track(name: string, props?: Props, options: TrackOptions = {}): void {
    if (name === "identify") {
      this.report(new Error('use identify() instead of track("identify")'));
      return;
    }
    const url = options.url !== undefined ? this.resolveUrl(options.url) : this.currentUrl ?? `${this.appUrl}/`;
    this.capture(name, url, { props, revenue: options.revenue });
  }

  identify(profileId: string | null, traits?: Traits): void {
    if (profileId === null || profileId === undefined) {
      this.reset();
      return;
    }
    if (!validProfileId(profileId)) {
      this.report(new Error("identify: profileId must be 1-128 characters"));
      return;
    }
    this.profileTouched = true;
    this.currentProfile = profileId;
    this.persist(this.keys.profile, profileId);
    this.capture("identify", this.currentUrl ?? `${this.appUrl}/`, { traits: sanitizeTraits(traits) });
  }

  reset(): void {
    this.profileTouched = true;
    this.currentProfile = undefined;
    this.persist(this.keys.profile, undefined);
  }

  async optOut(): Promise<void> {
    this.optOutTouched = true;
    this.optedOut = true;
    this.queue = [];
    await this.write(this.keys.optOut, "1");
    await this.write(this.keys.queue, undefined);
  }

  async optIn(): Promise<void> {
    this.optOutTouched = true;
    this.optedOut = false;
    await this.write(this.keys.optOut, undefined);
  }

  flush(): Promise<void> {
    if (this.retryTimer) {
      clearTimeout(this.retryTimer);
      this.retryTimer = undefined;
    }
    if (this.flushTimer) {
      clearTimeout(this.flushTimer);
      this.flushTimer = undefined;
    }
    if (!this.flushing) {
      this.flushing = this.drain().finally(() => {
        this.flushing = undefined;
      });
    }
    return this.flushing;
  }

  destroy(): void {
    this.destroyed = true;
    this.appStateSubscription?.remove();
    this.appStateSubscription = undefined;
    if (this.retryTimer) clearTimeout(this.retryTimer);
    if (this.flushTimer) clearTimeout(this.flushTimer);
    this.retryTimer = this.flushTimer = undefined;
  }

  // --- internals ---

  private resolveUrl(urlOrPath: string | undefined): string {
    if (urlOrPath && validEventUrl(urlOrPath)) return urlOrPath;
    const path = (urlOrPath ?? "").replace(/^\/+/, "");
    return `${this.appUrl}/${path}`;
  }

  private capture(name: string, url: string, extra: { props?: Props; revenue?: Revenue; traits?: Record<string, string> }): void {
    try {
      if (this.options.disabled || this.optedOut || this.destroyed) return;
      if (!validName(name)) throw new Error("event name must be 1-300 bytes");
      if (!validEventUrl(url)) throw new Error(`invalid event url: ${url}`);
      const event: EventPayload = { v: 1, id: newEventId(), ts: Date.now(), site: this.options.site, name, url };
      const width = screenWidth();
      if (width !== undefined) event.screen_width = width;
      const props = sanitizeProps(extra.props);
      if (props) event.props = props;
      if (name !== "pageview") {
        const revenue = sanitizeRevenue(extra.revenue);
        if (revenue) event.revenue = revenue;
      }
      if (this.currentProfile) event.profile_id = this.currentProfile;
      // Traits are only allowed on identify; elsewhere the server rejects the request.
      if (name === "identify" && extra.traits) event.traits = extra.traits;
      if (byteLength(JSON.stringify(event)) > MAX_BODY_BYTES) throw new Error("event body exceeds 8 KB");
      this.queue.push({ event, attempts: 0, early: !this.loaded && !this.profileTouched });
      const max = this.options.maxQueueSize ?? 500;
      if (this.queue.length > max) this.queue.splice(0, this.queue.length - max);
      this.scheduleFlush();
    } catch (error) {
      this.report(error);
    }
  }

  private scheduleFlush(): void {
    if (this.flushTimer || this.retryTimer || this.destroyed) return;
    this.flushTimer = setTimeout(() => {
      this.flushTimer = undefined;
      void this.flush();
    }, 0);
  }

  private async load(): Promise<void> {
    const storage = this.options.storage;
    try {
      if (!storage) return;
      const [profile, optOut, queued] = await Promise.all([
        storage.getItem(this.keys.profile),
        storage.getItem(this.keys.optOut),
        storage.getItem(this.keys.queue),
      ]);
      if (!this.profileTouched && profile && validProfileId(profile)) this.currentProfile = profile;
      if (!this.optOutTouched) this.optedOut = optOut === "1";
      if (queued && !this.optedOut) {
        try {
          const restored = (JSON.parse(queued) as EventPayload[])
            .filter((event) => event && typeof event.id === "string" && typeof event.ts === "number")
            .map((event) => ({ event, attempts: 0 }));
          this.queue = [...restored, ...this.queue];
        } catch {
          // Corrupt queue: ignore.
        }
        await storage.removeItem(this.keys.queue);
      }
    } catch (error) {
      this.report(error);
    } finally {
      this.loaded = true;
      if (this.optedOut) this.queue = [];
      for (const item of this.queue) {
        if (item.early && this.currentProfile && !item.event.profile_id) item.event.profile_id = this.currentProfile;
        item.early = false;
      }
    }
  }

  private async drain(): Promise<void> {
    await this.ready;
    while (this.queue.length > 0 && !this.optedOut) {
      const item = this.queue[0]!;
      if (Date.now() - item.event.ts > MAX_AGE_MS) {
        this.queue.shift();
        continue;
      }
      const outcome = await this.send(item.event);
      if (this.queue[0] !== item) continue; // queue was cleared (opt-out) meanwhile
      if (outcome.done) {
        this.queue.shift();
        continue;
      }
      item.attempts++;
      if (item.attempts >= (this.options.maxAttempts ?? 10)) {
        this.queue.shift();
        this.report(outcome.error ?? new Error("Supermetry event dropped after retries"));
        continue;
      }
      this.scheduleRetry(item.attempts, outcome.retryAfter);
      return;
    }
  }

  private scheduleRetry(attempts: number, retryAfterMs: number | undefined): void {
    if (this.destroyed) return;
    const base = this.options.retryDelay ?? 1000;
    const cap = this.options.maxRetryDelay ?? 60_000;
    const backoff = Math.min(cap, base * 2 ** (attempts - 1));
    const delay = Math.max(backoff * (0.8 + Math.random() * 0.4), retryAfterMs ?? 0);
    this.retryTimer = setTimeout(() => {
      this.retryTimer = undefined;
      void this.flush();
    }, delay);
  }

  /** `done` means remove from the queue (sent, or permanently rejected). */
  private async send(event: EventPayload): Promise<{ done: boolean; retryAfter?: number; error?: Error }> {
    try {
      const doFetch = this.options.fetch ?? globalThis.fetch;
      const response = await doFetch(this.endpoint, {
        method: "POST",
        headers: { "Content-Type": "application/json", "User-Agent": this.userAgent },
        body: JSON.stringify(event),
      });
      if (response.ok) return { done: true };
      const error = new SupermetryHttpError(response.status);
      if (response.status === 429 || response.status >= 500) {
        const seconds = Number(response.headers?.get?.("retry-after"));
        return { done: false, error, retryAfter: Number.isFinite(seconds) && seconds > 0 ? seconds * 1000 : undefined };
      }
      // 400 and other client errors will never succeed: drop.
      this.report(error);
      return { done: true, error };
    } catch (error) {
      return { done: false, error: toError(error) };
    }
  }

  private onAppState(state: AppStateStatus): void {
    if (state === "background" || state === "inactive") {
      void this.flush().then(() => this.persistQueue());
    } else if (state === "active" && this.queue.length > 0) {
      void this.flush().then(() => this.persistQueue());
    }
  }

  private async persistQueue(): Promise<void> {
    if (!this.options.storage || this.optedOut) return;
    await this.write(this.keys.queue, this.queue.length > 0 ? JSON.stringify(this.queue.map((item) => item.event)) : undefined);
  }

  private persist(key: string, value: string | undefined): void {
    void this.write(key, value);
  }

  private async write(key: string, value: string | undefined): Promise<void> {
    const storage = this.options.storage;
    if (!storage) return;
    try {
      if (value === undefined) await storage.removeItem(key);
      else await storage.setItem(key, value);
    } catch (error) {
      this.report(error);
    }
  }

  private report(error: unknown): void {
    try {
      this.options.onError?.(toError(error));
    } catch {
      // A throwing error hook must not break the app.
    }
  }
}

export function createSupermetry(options: SupermetryReactNativeOptions): SupermetryClient {
  return new Client(options);
}

export { VERSION };
