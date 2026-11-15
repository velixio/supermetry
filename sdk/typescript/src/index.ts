import { Client } from "./client.js";
import type { CaptureOptions, CrashFields, EventFields, Kind, Options } from "./types.js";

export { Client, VERSION } from "./client.js";
export { buildFrames, defaultInApp, parseV8Stack, scrubFrames, scrubPath } from "./frames.js";
export type * from "./types.js";

/** Creates an independent client (useful for tests and multiple apps). */
export function createClient(options: Options): Client {
  return new Client(options);
}

let current: Client | undefined;

function need(): Client {
  if (!current) throw new Error("supermetry: call init() first");
  return current;
}

export function init(options: Options): Client {
  current?.shutdown(0).catch(() => {});
  current = new Client(options);
  return current;
}
export const captureException = (err: unknown, options?: CaptureOptions) => need().captureException(err, options);
export const captureCrash = (fields?: CrashFields) => need().captureCrash(fields);
export const captureEvent = (kind: Kind, fields?: EventFields) => need().captureEvent(kind, fields);
export const startSession = () => need().startSession();
export const setEnabled = (on: boolean) => need().setEnabled(on);
export const flush = (timeoutMs?: number) => need().flush(timeoutMs);
export const shutdown = (timeoutMs?: number) => need().shutdown(timeoutMs);
