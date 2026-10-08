export interface TrackerEvent {
  site?: string;
  name: string;
  url?: string;
  referrer?: string;
  screen_width?: number;
  props?: Record<string, string>;
  engagement_ms?: number;
  scroll_pct?: number;
  revenue?: string;
  traits?: Record<string, string>;
  profile_id?: string;
}

export interface InitOptions {
  site: string;
  src: string;
  api?: string;
  manual?: boolean;
  hash?: boolean;
  consent?: boolean;
  beforeSend?: (event: TrackerEvent) => TrackerEvent | null;
  forms?: boolean;
  outbound?: boolean;
  downloads?: boolean;
  allowLocalhost?: boolean;
}

export interface PageviewOptions {
  url?: string;
  props?: Record<string, string>;
}

export interface Tracker {
  pageview(options?: PageviewOptions): void;
  track(name: string, props?: Record<string, string>): void;
  consent(granted: boolean): void;
}

interface PanelAPI {
  pageview?: Tracker["pageview"];
  track?: Tracker["track"];
  consent?: Tracker["consent"];
  beforeSend?: InitOptions["beforeSend"];
}

type Method = "pageview" | "track" | "consent";
type QueuedCall = [Method, unknown[]];

let singleton: Tracker | undefined;
let singletonDocument: Document | undefined;

const noopTracker = (): Tracker => ({
  pageview() {},
  track() {},
  consent() {},
});

function panelAPI(create = false): PanelAPI | undefined {
  const browserWindow = window as Window & { panel?: PanelAPI };
  if (create) return (browserWindow.panel ??= {});
  return browserWindow.panel;
}

function setBooleanAttribute(script: HTMLScriptElement, name: string, value: boolean | undefined): void {
  if (value !== undefined) script.setAttribute(`data-${name}`, String(value));
}

export function init(options: InitOptions): Tracker {
  if (typeof document === "undefined") return noopTracker();
  if (singleton && singletonDocument === document) return singleton;
  if (!options?.site || !options?.src) {
    throw new TypeError("@panel/tracker init requires site and src");
  }

  const script = document.createElement("script");
  script.async = true;
  script.defer = true;
  script.src = options.src;
  script.setAttribute("data-site", options.site);
  if (options.api !== undefined) script.setAttribute("data-api", options.api);
  setBooleanAttribute(script, "manual", options.manual);
  setBooleanAttribute(script, "hash", options.hash);
  if (options.consent) script.setAttribute("data-consent", "required");
  setBooleanAttribute(script, "forms", options.forms);
  setBooleanAttribute(script, "outbound", options.outbound);
  setBooleanAttribute(script, "downloads", options.downloads);
  if (options.allowLocalhost) script.setAttribute("data-allow-localhost", "");

  if (options.beforeSend) panelAPI(true)!.beforeSend = options.beforeSend;

  let loaded = false;
  const queue: QueuedCall[] = [];
  const invoke = (method: Method, args: unknown[]): void => {
    if (!loaded) {
      queue.push([method, args]);
      return;
    }
    const target = panelAPI()?.[method] as ((...values: unknown[]) => void) | undefined;
    target?.(...args);
  };

  singleton = {
    pageview: (pageviewOptions) => invoke("pageview", [pageviewOptions]),
    track: (name, props) => invoke("track", [name, props]),
    consent: (granted) => invoke("consent", [granted]),
  };
  singletonDocument = document;

  script.addEventListener("load", () => {
    loaded = true;
    for (const [method, args] of queue.splice(0)) invoke(method, args);
  });
  document.head.append(script);
  return singleton;
}
