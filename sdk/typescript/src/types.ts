export type Kind = "crash" | "error" | "hang" | "install_failed" | "install_stalled" | "incompatibility" | "operation_failed" | "session";
export type Level = "fatal" | "error" | "warning";
export type TagValue = string | number | boolean;

/** One event as sent on the wire (snake_case, see docs/guide/apps.md). */
export interface WireEvent {
  id: string;
  ts: number;
  kind: Kind;
  level?: Level;
  handled?: boolean;
  release: string;
  build?: string;
  environment?: string;
  platform: string;
  os_version?: string;
  arch: string;
  device_class?: string;
  runtime?: string;
  error_type?: string;
  exit_reason?: string;
  signal?: string;
  exit_code?: number;
  frames?: string;
  fingerprint?: string;
  occurrences?: number;
  install_id?: string;
  session_id?: string;
  tags?: Record<string, TagValue>;
}

export interface TransportRequest {
  url: string;
  headers: Record<string, string>;
  body: string;
}
export interface TransportResponse {
  status: number;
  body?: string;
}
export type Transport = (req: TransportRequest) => Promise<TransportResponse>;

export interface Storage {
  load(): WireEvent[] | Promise<WireEvent[]>;
  save(events: WireEvent[]): void | Promise<void>;
}

export type DropReason = string;

export interface Options {
  appId: string;
  ingestKey: string;
  host: string;
  release: string;
  build?: string;
  environment?: string;
  installId?: string;
  /** Consent switch. Defaults to true: pass false until the user has agreed. */
  enabled?: boolean;
  maxQueue?: number;
  flushIntervalMs?: number;
  maxAgeDays?: number;
  transport?: Transport;
  storage?: Storage;
  onDrop?: (events: WireEvent[], reason: DropReason) => void;
  /** Return true for first-party frame paths. Default drops node_modules, site-packages and runtime internals. */
  inApp?: (path: string) => boolean;
  osVersion?: string;
  deviceClass?: string;
  /** Test hooks. */
  sleep?: (ms: number) => Promise<void>;
  random?: () => number;
  now?: () => number;
}

export interface CaptureOptions {
  handled?: boolean;
  level?: Level;
  tags?: Record<string, TagValue>;
}

export interface EventFields {
  level?: Level;
  handled?: boolean;
  errorType?: string;
  exitReason?: string;
  signal?: string;
  exitCode?: number;
  frames?: string | string[];
  fingerprint?: string;
  occurrences?: number;
  deviceClass?: string;
  tags?: Record<string, TagValue>;
}

export interface CrashFields {
  errorType?: string;
  exitReason?: string;
  signal?: string;
  exitCode?: number;
  frames?: string | string[];
  tags?: Record<string, TagValue>;
}
