import { buildFrames, parseV8Stack, scrubFrames } from "./frames.js";
import { detectPlatform } from "./platform.js";
import type { CaptureOptions, CrashFields, EventFields, Kind, Options, TagValue, Transport, WireEvent } from "./types.js";

export const VERSION = "0.1.0";
const MAX_BATCH_EVENTS = 20;
const MAX_BODY_BYTES = 16 * 1024;
const BACKOFF_BASE_MS = 1000;
const BACKOFF_CAP_MS = 5 * 60 * 1000;
const DAY_MS = 86_400_000;
const enc = new TextEncoder();

const defaultTransport: Transport = async (req) => {
  const res = await fetch(req.url, { method: "POST", headers: req.headers, body: req.body });
  let body: string | undefined;
  try {
    body = await res.text();
  } catch {
    /* body is optional */
  }
  return { status: res.status, body };
};

function hex32(): string {
  const bytes = new Uint8Array(16);
  const c = (globalThis as any).crypto;
  if (c?.getRandomValues) c.getRandomValues(bytes);
  else for (let i = 0; i < 16; i++) bytes[i] = Math.floor(Math.random() * 256);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

function clip(s: string, max: number): string {
  return s.length > max ? s.slice(0, max) : s;
}

function cleanTags(tags: Record<string, TagValue> | undefined): Record<string, TagValue> | undefined {
  if (!tags) return undefined;
  const out: Record<string, TagValue> = {};
  let n = 0;
  for (const [k, v] of Object.entries(tags)) {
    if (n >= 20) break;
    if (typeof v === "string") {
      let s = v;
      while (enc.encode(s).length > 200) s = s.slice(0, s.length - 1);
      out[clip(k, 64)] = s;
    } else if (typeof v === "number" || typeof v === "boolean") out[clip(k, 64)] = v;
    else continue;
    n++;
  }
  return n ? out : undefined;
}

export class Client {
  private opts: Required<Pick<Options, "maxQueue" | "flushIntervalMs" | "maxAgeDays">> & Options;
  private queue: WireEvent[] = [];
  private enabled: boolean;
  private closed = false;
  private sessionId: string | undefined;
  private failures = 0;
  private nextAttemptAt = 0;
  private inflight: Promise<void> | null = null;
  private ready: Promise<void> = Promise.resolve();
  private timer: ReturnType<typeof setInterval> | undefined;
  private platform = detectPlatform();
  private transport: Transport;
  private now: () => number;
  private random: () => number;
  private sleep: (ms: number) => Promise<void>;

  constructor(options: Options) {
    this.opts = { maxQueue: 100, flushIntervalMs: 5000, maxAgeDays: 7, ...options };
    this.enabled = options.enabled ?? true;
    this.transport = options.transport ?? defaultTransport;
    this.now = options.now ?? Date.now;
    this.random = options.random ?? Math.random;
    this.sleep = options.sleep ?? ((ms) => new Promise((r) => setTimeout(r, ms)));
    if (options.storage && this.enabled) {
      this.ready = Promise.resolve(options.storage.load())
        .then((events) => {
          const valid = (events ?? []).filter((e) => e && typeof e.id === "string" && typeof e.ts === "number");
          this.queue = valid.concat(this.queue).slice(-this.opts.maxQueue);
        })
        .catch(() => {});
    }
    if (this.opts.flushIntervalMs > 0) {
      this.timer = setInterval(() => void this.tick(), this.opts.flushIntervalMs);
      (this.timer as any)?.unref?.();
    }
  }

  setEnabled(on: boolean): void {
    this.enabled = on;
    if (!on) {
      this.queue = [];
      this.persist();
    }
  }

  startSession(): void {
    if (this.sessionId) return;
    this.sessionId = hex32();
    this.enqueue("session", {});
  }

  captureException(err: unknown, options: CaptureOptions = {}): void {
    const isErr = err instanceof Error || (typeof err === "object" && err !== null && "stack" in err);
    const e = err as { name?: string; message?: string; stack?: string; constructor?: { name?: string } };
    let errorType = "Error";
    let frames = "";
    if (isErr) {
      const ctor = e.constructor?.name;
      errorType = ctor && ctor !== "Object" ? ctor : e.name || "Error";
      if (typeof e.stack === "string") {
        frames = buildFrames(parseV8Stack(e.stack, typeof e.message === "string" ? e.message : ""), this.opts.inApp);
      }
    }
    this.enqueue("error", { errorType, frames, handled: options.handled ?? true, level: options.level, tags: options.tags });
  }

  captureCrash(fields: CrashFields = {}): void {
    this.enqueue("crash", { handled: false, ...fields, level: "fatal" });
  }

  captureEvent(kind: Kind, fields: EventFields = {}): void {
    this.enqueue(kind, fields);
  }

  async flush(timeoutMs = 10_000): Promise<void> {
    await this.ready;
    if (!this.enabled) return;
    if (!this.inflight) {
      const deadline = Date.now() + timeoutMs;
      this.inflight = this.drain(deadline, true).finally(() => (this.inflight = null));
    }
    await this.inflight;
  }

  async shutdown(timeoutMs = 10_000): Promise<void> {
    await this.flush(timeoutMs);
    this.closed = true;
    if (this.timer) clearInterval(this.timer);
    this.queue = [];
  }

  private async tick(): Promise<void> {
    if (this.inflight || !this.enabled || this.closed || !this.queue.length || this.now() < this.nextAttemptAt) return;
    await this.ready;
    this.inflight = this.drain(Date.now(), false).finally(() => (this.inflight = null));
    await this.inflight;
  }

  private enqueue(kind: Kind, f: EventFields): void {
    if (!this.enabled || this.closed) return;
    const o = this.opts;
    const e: WireEvent = {
      id: hex32(),
      ts: this.now(),
      kind,
      release: clip(o.release, 64),
      platform: this.platform.platform,
      arch: this.platform.arch,
      runtime: this.platform.runtime,
    };
    const osv = o.osVersion ?? this.platform.osVersion;
    if (osv) e.os_version = clip(osv, 32);
    if (o.build) e.build = clip(o.build, 64);
    if (o.environment) e.environment = clip(o.environment, 32);
    if (o.installId) e.install_id = clip(o.installId, 128);
    if (this.sessionId) e.session_id = this.sessionId;
    const dc = f.deviceClass ?? o.deviceClass;
    if (dc) e.device_class = clip(dc, 32);
    if (kind !== "session") {
      e.level = f.level ?? (kind === "crash" ? "fatal" : "error");
      e.handled = f.handled ?? kind !== "crash";
    }
    if (f.errorType) e.error_type = clip(f.errorType, 64);
    if (f.exitReason) e.exit_reason = f.exitReason;
    if (f.signal) e.signal = f.signal;
    if (typeof f.exitCode === "number") e.exit_code = f.exitCode;
    if (f.frames) {
      const text = Array.isArray(f.frames) ? f.frames.join("\n") : f.frames;
      const scrubbed = scrubFrames(text);
      if (scrubbed) e.frames = scrubbed;
    }
    if (f.fingerprint) e.fingerprint = f.fingerprint;
    if (f.occurrences && f.occurrences > 1) e.occurrences = Math.floor(f.occurrences);
    const tags = cleanTags(f.tags);
    if (tags) e.tags = tags;
    this.queue.push(e);
    while (this.queue.length > o.maxQueue) this.drop([this.queue.shift()!], "queue_full");
    this.persist();
  }

  private drop(events: WireEvent[], reason: string): void {
    try {
      this.opts.onDrop?.(events, reason);
    } catch {
      /* hooks must not break the app */
    }
  }

  private persist(): void {
    try {
      void Promise.resolve(this.opts.storage?.save(this.queue.slice())).catch(() => {});
    } catch {
      /* storage is best effort */
    }
  }

  private nextBatch(): { events: WireEvent[]; body: string } | null {
    const prefix = `{"site":${JSON.stringify(this.opts.appId)},"events":[`;
    const suffix = "]}";
    let size = enc.encode(prefix + suffix).length;
    const picked: string[] = [];
    const events: WireEvent[] = [];
    for (const e of this.queue) {
      if (events.length >= MAX_BATCH_EVENTS) break;
      const json = JSON.stringify(e);
      const add = enc.encode(json).length + (events.length ? 1 : 0);
      if (size + add > MAX_BODY_BYTES) {
        if (!events.length) {
          this.queue.shift();
          this.drop([e], "too_large");
          return this.nextBatch();
        }
        break;
      }
      size += add;
      picked.push(json);
      events.push(e);
    }
    return events.length ? { events, body: prefix + picked.join(",") + suffix } : null;
  }

  private backoffMs(): number {
    const base = Math.min(BACKOFF_CAP_MS, BACKOFF_BASE_MS * 2 ** Math.max(0, this.failures - 1));
    const jitter = 1 + (this.random() - 0.5) * 0.5;
    return Math.min(BACKOFF_CAP_MS, Math.round(base * jitter));
  }

  private async drain(deadline: number, wait: boolean): Promise<void> {
    while (this.enabled && this.queue.length) {
      const cutoff = this.now() - this.opts.maxAgeDays * DAY_MS;
      const old = this.queue.filter((e) => e.ts < cutoff);
      if (old.length) {
        this.queue = this.queue.filter((e) => e.ts >= cutoff);
        this.drop(old, "too_old");
        this.persist();
      }
      const batch = this.nextBatch();
      if (!batch) break;
      const ids = new Set(batch.events.map((e) => e.id));
      const remove = () => {
        this.queue = this.queue.filter((e) => !ids.has(e.id));
        this.persist();
      };
      let status = 0;
      let reply: string | undefined;
      try {
        const res = await this.transport({
          url: this.opts.host.replace(/\/+$/, "") + "/api/public/v1/app-events",
          headers: {
            "Content-Type": "application/json",
            "X-Supermetry-Key": this.opts.ingestKey,
            "User-Agent": `supermetry-js/${VERSION}`,
          },
          body: batch.body,
        });
        status = res.status;
        reply = res.body;
      } catch {
        status = 0;
      }
      if (status >= 200 && status < 300) {
        this.failures = 0;
        remove();
        this.reportServerDrops(batch.events, reply);
      } else if (status >= 400 && status < 500 && status !== 429 && status !== 408) {
        remove();
        this.drop(batch.events, `http_${status}`);
      } else {
        this.failures++;
        const delay = this.backoffMs();
        this.nextAttemptAt = this.now() + delay;
        if (!wait || Date.now() + delay > deadline) return;
        await this.sleep(delay);
      }
    }
  }

  private reportServerDrops(events: WireEvent[], reply: string | undefined): void {
    if (!reply) return;
    try {
      const parsed = JSON.parse(reply) as { dropped?: { index: number; reason: string }[] };
      for (const d of parsed.dropped ?? []) {
        const ev = events[d.index];
        if (ev) this.drop([ev], String(d.reason));
      }
    } catch {
      /* ignore malformed replies */
    }
  }
}
