export type PropValue = string | number | boolean;
export type Props = Record<string, PropValue>;
export type Traits = Record<string, string>;

export interface Revenue {
  amount: string | number;
  currency?: string;
}

export interface TrackerEvent {
  site?: string;
  name: string;
  url?: string;
  referrer?: string;
  screen_width?: number;
  props?: Props;
  engagement_ms?: number;
  scroll_pct?: number;
  revenue?: Revenue;
  traits?: Traits;
  profile_id?: string;
}

export interface TrackerOptions {
  api?: string;
  manual?: boolean;
  hash?: boolean;
  consent?: boolean;
  beforeSend?: (event: TrackerEvent) => TrackerEvent | null;
  forms?: boolean;
  outbound?: boolean;
  downloads?: boolean;
  profiles?: boolean;
  allowLocalhost?: boolean;
}

export type InitOptions = TrackerOptions & { site: string } & (
    | { src: string; host?: string }
    | { src?: string; host: string }
  );

export interface PageviewOptions {
  url?: string;
  props?: Props;
}

export interface TrackOptions {
  revenue?: Revenue;
}

export interface Tracker {
  pageview(options?: PageviewOptions): void;
  track(name: string, props?: Props, options?: TrackOptions): void;
  identify(id: string | null, traits?: Traits): void;
  consent(granted: boolean): void;
  optIn(): void;
  optOut(): void;
}

type Method = keyof Tracker;
type QueuedCall = [Method, unknown[]];
type SupermetryAPI = Partial<Record<Method, (...args: unknown[]) => void>> & {
  beforeSend?: TrackerOptions["beforeSend"];
};

const maxQueue = 100;
const flushRetries = 200;

interface State {
  document: Document;
  initialized: boolean;
  polling: boolean;
  queue: QueuedCall[];
}

let current: State | undefined;

function state(): State | undefined {
  if (typeof document === "undefined") return undefined;
  if (current?.document !== document) {
    current = { document, initialized: false, polling: false, queue: [] };
  }
  return current;
}

function supermetryAPI(create = false): SupermetryAPI | undefined {
  const browserWindow = window as Window & { supermetry?: SupermetryAPI };
  if (create) return (browserWindow.supermetry ??= {});
  return browserWindow.supermetry;
}

function ready(): SupermetryAPI | undefined {
  const api = supermetryAPI();
  return typeof api?.track === "function" ? api : undefined;
}

function invoke(method: Method, args: unknown[]): void {
  const current = state();
  if (!current) return;
  const api = ready();
  if (api) {
    for (const [queued, queuedArgs] of current.queue.splice(0)) api[queued]?.(...queuedArgs);
    api[method]?.(...args);
    return;
  }
  if (current.queue.length >= maxQueue) current.queue.shift();
  current.queue.push([method, args]);
  if (!current.initialized) poll(current);
}

function poll(current: State): void {
  if (current.polling) return;
  current.polling = true;
  flush(current, flushRetries);
}

function flush(current: State, retries: number): void {
  if (current !== state()) return;
  const api = ready();
  if (!api) {
    if (retries > 0) setTimeout(() => flush(current, retries - 1), 50);
    else current.polling = false;
    return;
  }
  current.polling = false;
  for (const [method, args] of current.queue.splice(0)) api[method]?.(...args);
}

const tracker: Tracker = {
  pageview: (options) => invoke("pageview", [options]),
  track: (name, props, options) => invoke("track", [name, props, options]),
  identify: (id, traits) => invoke("identify", traits === undefined ? [id] : [id, traits]),
  consent: (granted) => invoke("consent", [granted]),
  optIn: () => invoke("optIn", []),
  optOut: () => invoke("optOut", []),
};

export function getTracker(): Tracker {
  return tracker;
}

function setBooleanAttribute(script: HTMLScriptElement, name: string, value: boolean | undefined): void {
  if (value !== undefined) script.setAttribute(`data-${name}`, String(value));
}

export function scriptSource(options: InitOptions): string {
  if (options.src) return options.src;
  const full = options.manual || options.consent || options.beforeSend;
  return `${trimSlash(options.host ?? "")}/js/script${full ? ".full" : ""}.js`;
}

export function eventsEndpoint(options: InitOptions): string | undefined {
  if (options.api !== undefined) return options.api;
  if (options.host === undefined) return undefined;
  return `${trimSlash(options.host)}/api/public/v1/events`;
}

function trimSlash(value: string): string {
  return value.replace(/\/+$/, "");
}

export function init(options: InitOptions): Tracker {
  const current = state();
  if (!current) return tracker;
  if (current.initialized) return tracker;
  if (!options?.site || (!options.src && typeof options.host !== "string")) {
    throw new TypeError("@supermetry/tracker init requires site and src or host");
  }

  const script = document.createElement("script");
  script.async = true;
  script.defer = true;
  script.src = scriptSource(options);
  script.setAttribute("data-site", options.site);
  const api = eventsEndpoint(options);
  if (api !== undefined) script.setAttribute("data-api", api);
  setBooleanAttribute(script, "manual", options.manual);
  setBooleanAttribute(script, "hash", options.hash);
  if (options.consent) script.setAttribute("data-consent", "required");
  setBooleanAttribute(script, "forms", options.forms);
  setBooleanAttribute(script, "outbound", options.outbound);
  setBooleanAttribute(script, "downloads", options.downloads);
  setBooleanAttribute(script, "profiles", options.profiles);
  if (options.allowLocalhost) script.setAttribute("data-allow-localhost", "");

  if (options.beforeSend) supermetryAPI(true)!.beforeSend = options.beforeSend;

  current.initialized = true;
  script.addEventListener("load", () => {
    current.polling = false;
    poll(current);
  });
  document.head.append(script);
  return tracker;
}
