import { Supermetry, SupermetryError, type EventContext, type PageviewOptions, type SendResult, type SupermetryOptions, type Traits, type TrackOptions } from "@supermetry/node";
import type { NextFunction, Request, RequestHandler, Response } from "express";

export { Supermetry, SupermetryError } from "@supermetry/node";
export type { EventContext, EventPayload, PageviewOptions, Props, Revenue, SendResult, SupermetryOptions, Traits, TrackOptions } from "@supermetry/node";

/** Tracking methods bound to the current request's visitor (UA, IP, URL, referrer). */
export interface RequestTracker {
  track(name: string, options?: TrackOptions): Promise<SendResult>;
  pageview(options?: PageviewOptions): Promise<SendResult>;
  /** Sends identify and attaches `profileId` to later calls on this request only. */
  identify(profileId: string, traits?: Traits, context?: EventContext): Promise<SendResult>;
  /** The request context that is merged into every call (per-call options win). */
  readonly context: EventContext;
  readonly client: Supermetry;
}

export interface SupermetryMiddlewareOptions extends Partial<SupermetryOptions> {
  /** Reuse an existing client instead of creating one from `site`/`host`. */
  client?: Supermetry;
  /**
   * Send a pageview when the response finishes. `true` tracks GET requests answered
   * 2xx with `text/html`; a function decides per request.
   */
  trackPageviews?: boolean | ((req: Request, res: Response) => boolean);
  /** Builds the event URL. Defaults to `req.protocol + "://" + req.get("host") + req.originalUrl`. */
  getUrl?: (req: Request) => string | undefined;
  /** Returns the logged-in user's profile ID so every event carries `profile_id`. */
  getProfileId?: (req: Request) => string | undefined | null;
}

declare global {
  // eslint-disable-next-line @typescript-eslint/no-namespace
  namespace Express {
    interface Request {
      /** Added by the `@supermetry/express` middleware. */
      supermetry: RequestTracker;
    }
  }
}

export function requestUrl(req: Request): string | undefined {
  const host = req.get("host");
  if (!host) return undefined;
  return `${req.protocol}://${host}${req.originalUrl ?? req.url}`;
}

export function requestContext(req: Request, options: Pick<SupermetryMiddlewareOptions, "getUrl" | "getProfileId"> = {}): EventContext {
  const context: EventContext = {};
  const url = options.getUrl ? options.getUrl(req) : requestUrl(req);
  if (url) context.url = url;
  const userAgent = req.get("user-agent");
  if (userAgent) context.userAgent = userAgent;
  const ip = req.ip ?? req.socket?.remoteAddress;
  if (ip) context.ip = ip;
  const referrer = req.get("referer");
  if (referrer) context.referrer = referrer;
  const profileId = options.getProfileId?.(req);
  if (profileId) context.profileId = profileId;
  return context;
}

function bind(client: Supermetry, context: EventContext): RequestTracker {
  return {
    context,
    client,
    track: (name, options = {}) => client.track(name, { ...context, ...options }),
    pageview: (options = {}) => client.pageview({ ...context, ...options }),
    identify: (profileId, traits, extra = {}) => {
      // Scoped to this request only: the shared client never stores a profile ID.
      if (profileId) context.profileId = profileId;
      return client.identify(profileId, traits, { ...context, ...extra });
    },
  };
}

function isHtmlPageview(req: Request, res: Response): boolean {
  if (req.method !== "GET" || res.statusCode < 200 || res.statusCode >= 300) return false;
  const type = res.getHeader("content-type");
  return typeof type === "string" && type.toLowerCase().includes("text/html");
}

/** Express middleware that attaches `req.supermetry`. */
export function supermetry(options: SupermetryMiddlewareOptions): RequestHandler {
  const { client: provided, trackPageviews, getUrl, getProfileId, ...clientOptions } = options;
  const client = provided ?? new Supermetry(clientOptions as SupermetryOptions);

  return function supermetryMiddleware(req: Request, res: Response, next: NextFunction) {
    let tracker: RequestTracker;
    try {
      tracker = bind(client, requestContext(req, { getUrl, getProfileId }));
    } catch (error) {
      // Analytics must never break a request.
      tracker = bind(client, {});
      try {
        clientOptions.onError?.(new SupermetryError("failed to read request context", { cause: error }));
      } catch {
        // ignore
      }
    }
    req.supermetry = tracker;

    if (trackPageviews) {
      res.once("finish", () => {
        try {
          const send = typeof trackPageviews === "function" ? trackPageviews(req, res) : isHtmlPageview(req, res);
          if (send) void tracker.pageview();
        } catch {
          // Ignore predicate errors.
        }
      });
    }
    next();
  };
}

export default supermetry;
