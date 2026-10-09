import { record, type eventWithTime } from "rrweb";

type ReplayConfig = {
  s: string;
  o: string;
  v?: string;
};

type StartResponse = {
  m: "full" | "trigger" | "off";
  mask: boolean;
  v: string;
  t: { e: boolean; r: boolean; u: string[]; x: boolean };
};

declare global {
  interface Window {
    panel?: {
      _replay?: ReplayConfig;
      _replayTrigger?: string;
      _replayDenied?: number | boolean;
      _replayConsent?: boolean;
      _allowed?: () => boolean;
      _replayStop?: () => void;
      replay?: { trigger(reason?: string): void };
    };
  }
}

const panel = window.panel;
const config = panel?._replay;

if (panel && config) void start(panel, config);

async function start(panel: NonNullable<Window["panel"]>, config: ReplayConfig) {
  if (!allowed(panel)) return;
  let response: Response;
  try {
    response = await fetch(config.o + "/api/replay/start", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ s: config.s, u: location.href, c: !!panel._replayConsent }),
    });
  } catch {
    panel._replay = undefined;
    return;
  }
  if (!response.ok) {
    panel._replay = undefined;
    return;
  }

  const startConfig = await response.json() as StartResponse;
  if (startConfig.m === "off") return;
  if (!allowed(panel)) {
    panel._replay = undefined;
    return;
  }

  config.v = startConfig.v;
  let mode = startConfig.m;
  let sequence = 0;
  let firstChunk = true;
  let stopped = false;
  let totalBytes = 0;
  let pending: eventWithTime[] = [];
  let pendingBytes = 0;
  const recordedAt = Date.now();
  let ringSegments: eventWithTime[][] = [[]];
  let ringSizes = [0];
  let ringStarted = recordedAt;
  let ringMeta: eventWithTime | undefined;
  let checkpointing = false;
  let flushChain = Promise.resolve();
  let cancelUploads = false;
  let interval: ReturnType<typeof setInterval> | undefined;
  let lastActivity = recordedAt;
  let eventChain = Promise.resolve();
  const sentCSS = new Set<string>();

  const maskInputFn = (text: string, element: HTMLElement) => {
    const input = element as HTMLInputElement;
    const sensitive = input.type === "password" || input.type === "hidden" ||
      (input.autocomplete || "").toLowerCase().startsWith("cc-");
    return startConfig.mask || sensitive ? "*".repeat(text.length) : text;
  };

  let stop: ReturnType<typeof record>;
  stop = record({
    emit(event) {
      if (stopped || !allowed(panel)) return;
      eventChain = eventChain.then(async () => {
        if (cancelUploads || !allowed(panel)) return;
        await deduplicateSnapshotCSS(event, sentCSS, config, panel, () => !cancelUploads && allowed(panel));
        if (cancelUploads || !allowed(panel)) return;
        acceptEvent(event);
      });
    },
    sampling: { mousemove: 100, scroll: 150, input: "last" },
    recordCanvas: false,
    inlineImages: false,
    collectFonts: false,
    checkoutEveryNms: 300_000,
    maskAllInputs: startConfig.mask,
    maskTextSelector: startConfig.mask ? "*" : undefined,
    maskInputOptions: {
      color: true, date: true, "datetime-local": true, email: true, hidden: true,
      month: true, number: true, password: true, range: true, search: true,
      select: true, tel: true, text: true, textarea: true, time: true, url: true, week: true,
    },
    maskInputFn,
  });
  if (stopped) {
    stop?.();
    return;
  }

  function acceptEvent(event: eventWithTime) {
    if (cancelUploads || !allowed(panel)) { halt(false, true); return; }
    if (Date.now() - recordedAt >= 30 * 60_000 || Date.now() - lastActivity >= 2 * 60_000 || totalBytes >= 5 * 1024 * 1024) {
      halt(true);
      return;
    }

    const size = JSON.stringify(event).length + 1;
    if (mode === "trigger") {
      if (size > 5 * 1024 * 1024) {
        halt(false, true);
        return;
      }
      if (event.type === 4) ringMeta = event;
      const ringBytes = ringSizes.reduce((sum, bytes) => sum + bytes, 0);
      if (!checkpointing && (Date.now() - ringStarted >= 60_000 || ringBytes + size > 5 * 1024 * 1024)) {
        checkpointing = true;
        ringStarted = Date.now();
        ringSegments.push([]);
        ringSizes.push(0);
        if (ringMeta) addToRing({ ...ringMeta, timestamp: event.timestamp });
        record.takeFullSnapshot();
        checkpointing = false;
        while (ringSegments.length > 2 || ringSegments.length > 1 && ringSizes.reduce((sum, bytes) => sum + bytes, 0) > 5 * 1024 * 1024) {
          ringSegments.shift();
          ringSizes.shift();
        }
        if (ringSizes.reduce((sum, bytes) => sum + bytes, 0) > 5 * 1024 * 1024) halt(false, true);
        return;
      }
      addToRing(event, size);
      return;
    }

    pending.push(event);
    pendingBytes += size;
    if (pendingBytes >= 64 * 1024) queueFlush(false);
  }

  function addToRing(event: eventWithTime, size = JSON.stringify(event).length + 1) {
    ringSegments[ringSegments.length - 1].push(event);
    ringSizes[ringSizes.length - 1] += size;
  }

  function queueFlush(keepalive: boolean, trigger?: string, events?: eventWithTime[]) {
    const readyEvents = eventChain;
    const startsPage = firstChunk;
    firstChunk = false;
    flushChain = Promise.all([flushChain, readyEvents]).then(async () => {
      const batch = events ?? pending;
      if (!events) {
        pending = [];
        pendingBytes = 0;
      }
      if (!batch.length) return;
      const plain = batch.map((event) => JSON.stringify(event)).join("\n") + "\n";
      const compressed = await gzip(plain);
      if (cancelUploads || !allowed(panel)) return;
      if (totalBytes + compressed.byteLength > 5 * 1024 * 1024) {
        halt(false, true);
        return;
      }
      const query = new URLSearchParams({
        s: config.s,
        q: String(sequence++),
        p: startsPage ? "1" : "0",
        c: panel._replayConsent ? "1" : "0",
        v: startConfig.v,
      });
      if (trigger) query.set("t", trigger);
      totalBytes += compressed.byteLength;
      await fetch(config.o + "/api/replay/chunk?" + query, {
        method: "POST",
        headers: { "Content-Type": "application/octet-stream" },
        body: compressed,
        keepalive,
      }).catch(() => undefined);
    });
  }

  function queueBatches(events: eventWithTime[], keepalive: boolean, trigger?: string) {
    let batch: eventWithTime[] = [];
    let bytes = 0;
    for (const event of events) {
      const size = JSON.stringify(event).length + 1;
      if (batch.length && bytes + size > 64 * 1024) {
        queueFlush(keepalive, trigger, batch);
        trigger = undefined;
        batch = [];
        bytes = 0;
      }
      batch.push(event);
      bytes += size;
    }
    queueFlush(keepalive, trigger, batch);
  }

  function halt(flush: boolean, discard = false) {
    if (discard) { cancelUploads = true; pending = []; pendingBytes = 0; ringSegments = [[]]; ringSizes = [0]; }
    if (stopped) return;
    if (flush && mode === "full") queueFlush(true);
    if (discard) cancelUploads = true;
    stopped = true;
    if (discard) {
      pending = [];
      pendingBytes = 0;
    }
    ringSegments = [[]];
    ringSizes = [0];
    stop?.();
    if (interval) clearInterval(interval);
    if (discard) {
      panel._replay = undefined;
      panel._replayStop = undefined;
    }
  }

  function trigger(reason = "manual") {
    if (!allowed(panel)) { halt(false, true); return; }
    if (stopped || mode !== "trigger") return;
    const buffered = ringSegments.flat();
    ringSegments = [[]];
    ringSizes = [0];
    mode = "full";
    queueBatches(buffered, false, reason);
  }

  panel.replay = { trigger: (reason) => { if (startConfig.t.x) trigger(reason); } };
  panel._replayStop = () => halt(false, true);
  if (panel._replayTrigger && startConfig.t.x) trigger(panel._replayTrigger);

  if (startConfig.t.e) {
    addEventListener("error", () => trigger("error"));
    addEventListener("unhandledrejection", () => trigger("error"));
  }
  if (startConfig.t.r) {
    const clicks: Array<{ x: number; y: number; at: number }> = [];
    addEventListener("click", (event) => {
      lastActivity = Date.now();
      clicks.push({ x: event.clientX, y: event.clientY, at: lastActivity });
      while (clicks.length && lastActivity - clicks[0].at > 1_000) clicks.shift();
      if (clicks.length >= 3) {
        const first = clicks[clicks.length - 3];
        const near = clicks.slice(-3).every((click) => Math.hypot(click.x - first.x, click.y - first.y) <= 30);
        if (near) trigger("rage-click");
      }
    }, { passive: true });
  }
  for (const event of ["pointerdown", "keydown", "scroll"] as const) {
    addEventListener(event, () => { lastActivity = Date.now(); }, { passive: true });
  }
  const matchesURLTrigger = () => startConfig.t.u.some((pattern) => glob(pattern).test(location.href));
  if (matchesURLTrigger()) trigger("url");

  let pageURL = location.href;
  const navigate = () => {
    if (stopped) return;
    if (location.href === pageURL) return;
    if (mode === "full") queueFlush(false);
    firstChunk = true;
    pageURL = location.href;
    record.takeFullSnapshot();
    if (matchesURLTrigger()) trigger("url");
  };
  for (const method of ["pushState", "replaceState"] as const) {
    const original = history[method];
    history[method] = function (...args: Parameters<History[typeof method]>) {
      const result = original.apply(history, args);
      navigate();
      return result;
    };
  }
  addEventListener("popstate", navigate);

  interval = setInterval(() => {
    if (Date.now() - recordedAt >= 30 * 60_000 || Date.now() - lastActivity >= 2 * 60_000) {
      halt(true);
      return;
    }
    queueFlush(false);
  }, 10_000);
  document.addEventListener("visibilitychange", () => {
    if (document.hidden && mode === "full") queueFlush(true);
  });
  addEventListener("pagehide", () => {
    if (mode === "trigger") {
      halt(false);
      return;
    }
    queueFlush(true);
    halt(false);
  }, { once: true });
}

async function gzip(text: string): Promise<Uint8Array> {
  const stream = new Blob([text]).stream().pipeThrough(new CompressionStream("gzip"));
  return new Uint8Array(await new Response(stream).arrayBuffer());
}

type SnapshotNode = {
  attributes?: Record<string, unknown>;
  childNodes?: SnapshotNode[];
};

async function deduplicateSnapshotCSS(event: eventWithTime, sent: Set<string>, config: ReplayConfig, panel: NonNullable<Window["panel"]>, canUpload: () => boolean) {
  if (event.type !== 2) return;
  const root = (event.data as { node?: SnapshotNode }).node;
  if (!root) return;
  const nodes: SnapshotNode[] = [root];
  while (nodes.length) {
    const node = nodes.pop()!;
    if (node.childNodes) nodes.push(...node.childNodes);
    const css = node.attributes?._cssText;
    if (typeof css !== "string" || css.length <= 1024) continue;
    try {
      const hash = await sha256hex(css);
      if (!sent.has(hash)) {
        const body = await gzip(css);
        if (!canUpload()) return;
        const response = await fetch(config.o + "/api/replay/css/" + hash + "?s=" + encodeURIComponent(config.s) + "&c=" + (panel._replayConsent ? "1" : "0") + "&v=" + encodeURIComponent(config.v || ""), {
          method: "PUT",
          headers: { "Content-Type": "application/gzip" },
          body,
        });
        if (!response.ok) continue;
        sent.add(hash);
      }
      node.attributes!._cssRef = hash;
      delete node.attributes!._cssText;
    } catch {
      // Keep the stylesheet inline so recording remains playable.
    }
  }
}

async function sha256hex(value: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(value));
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

function glob(pattern: string): RegExp {
  const escaped = pattern.replace(/[.+?^${}()|[\]\\]/g, "\\$&").replaceAll("*", ".*");
  return new RegExp("^" + escaped + "$");
}

function allowed(panel: NonNullable<Window["panel"]>): boolean {
  return !panel._replayDenied && !(navigator as Navigator & { globalPrivacyControl?: boolean }).globalPrivacyControl && navigator.doNotTrack !== "1" && (panel._allowed?.() ?? true);
}
