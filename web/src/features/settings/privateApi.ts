import { registerPrivateTask } from "@/lib/session-lifecycle";
import { api } from "@/lib/api";

type Method = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
type ApiResult = { data?: unknown; error?: unknown; response: Response };
type LooseClient = Record<
  Method,
  (path: string, options: Record<string, unknown>) => Promise<ApiResult>
>;

export async function privateRequest<T>(
  method: Method,
  path: string,
  options: { orgId?: number; body?: unknown } = {},
): Promise<T> {
  const client = api as unknown as LooseClient;
  const headers = options.orgId
    ? { "X-Supermetry-Org": String(options.orgId) }
    : undefined;
  const result = await client[method](path, {
    body: options.body,
    params: headers ? { header: headers } : undefined,
  });
  if (!result.response.ok) {
    const problem = result.error as { detail?: string } | undefined;
    throw new Error(
      problem?.detail ?? `Request failed (${result.response.status})`,
    );
  }
  return result.data as T;
}

export type ListResponse<T> = { data: T[]; next_cursor?: string | null };

export type TOTPReauthentication = {
  current_password: string;
  code: string;
};

export type TOTPEnrollment = {
  url: string;
  recovery_codes: string[];
};

export function enrollTOTP(body: TOTPReauthentication): Promise<TOTPEnrollment> {
  return privateRequest("POST", "/api/private/me/totp", { body });
}

export function disableTOTP(body: TOTPReauthentication): Promise<void> {
  return privateRequest("DELETE", "/api/private/me/totp", { body });
}

export function regenerateTOTPRecoveryCodes(body: TOTPReauthentication): Promise<{ recovery_codes: string[] }> {
  return privateRequest("POST", "/api/private/me/totp/recovery-codes", { body });
}

export type ImportSource = "plausible" | "umami" | "ga4" | "mixpanel" | "amplitude";

function cookie(name: string): string | undefined {
  const prefix = `${encodeURIComponent(name)}=`;
  const match = document.cookie
    .split(";")
    .map((part) => part.trim())
    .find((part) => part.startsWith(prefix));
  if (!match) return undefined;
  try {
    return decodeURIComponent(match.slice(prefix.length));
  } catch {
    return undefined;
  }
}

export function uploadSiteImport<T>(
  siteId: number,
  orgId: number,
  source: ImportSource,
  file: File,
  onProgress: (percent: number) => void,
  register: typeof registerPrivateTask = registerPrivateTask,
  replace = false,
): Promise<T> {
  return new Promise((resolve, reject) => {
    const request = new XMLHttpRequest();
    let settled = false;
    let unregister = () => {};
    const cleanup = () => {
      settled = true;
      unregister();
      request.upload.removeEventListener("progress", progress);
      request.removeEventListener("load", load);
      request.removeEventListener("error", error);
      request.removeEventListener("abort", abort);
    };
    const fail = (reason: unknown) => {
      if (settled) return;
      cleanup();
      reject(reason);
    };
    const progress = (event: ProgressEvent) => {
      if (!settled && event.lengthComputable) {
        onProgress(Math.round((event.loaded / event.total) * 100));
      }
    };
    const load = () => {
      if (settled) return;
      if (request.status >= 200 && request.status < 300) {
        try {
          const result = JSON.parse(request.responseText) as T;
          cleanup();
          resolve(result);
        } catch {
          fail(new Error("The server returned an invalid import response"));
        }
        return;
      }
      fail(new Error(problemDetail(request.responseText) ?? `Upload failed (${request.status})`));
    };
    const error = () => fail(new Error("Upload failed"));
    const abort = () => fail(new DOMException("Session changed", "AbortError"));
    try {
      unregister = register(() => {
        abort();
        request.abort();
      });
      request.open("POST", `/api/private/sites/${siteId}/imports${replace ? "?replace=true" : ""}`);
      request.setRequestHeader("X-Supermetry-Org", String(orgId));
      const csrf = cookie("supermetry_csrf");
      if (csrf) request.setRequestHeader("X-CSRF-Token", csrf);
      request.upload.addEventListener("progress", progress);
      request.addEventListener("load", load);
      request.addEventListener("error", error);
      request.addEventListener("abort", abort);
      const body = new FormData();
      body.set("source", source);
      body.set("file", file);
      request.send(body);
    } catch (reason) {
      fail(reason);
    }
  });
}

function problemDetail(text: string): string | undefined {
  try {
    const detail = (JSON.parse(text) as { detail?: unknown }).detail;
    return typeof detail === "string" && detail ? detail : undefined;
  } catch {
    return undefined;
  }
}

export type ImportRowError = { row: number; message: string };

export type ImportPreview = {
  source: ImportSource;
  file_name: string;
  rows: number;
  bad_rows: number;
  duplicate_rows: number;
  errors: ImportRowError[];
  days: number;
  date_from?: string;
  date_to?: string;
  totals: { visitors: number; visits: number; pageviews: number; events: number };
  dimensions: Record<string, number>;
  overlap: {
    imported_days: number;
    live_days: number;
    same_file: { import_id: number; imported_at: string } | null;
  };
};

/** Dry run: parses the file on the server and reports what an import would add. */
export async function previewSiteImport(
  siteId: number,
  orgId: number,
  source: ImportSource,
  file: File,
  register: typeof registerPrivateTask = registerPrivateTask,
): Promise<ImportPreview> {
  const controller = new AbortController();
  const unregister = register(() => controller.abort());
  try {
    const headers = new Headers({ "X-Supermetry-Org": String(orgId) });
    const csrf = cookie("supermetry_csrf");
    if (csrf) headers.set("X-CSRF-Token", csrf);
    const body = new FormData();
    body.set("source", source);
    body.set("file", file);
    const response = await fetch(`/api/private/sites/${siteId}/imports/preview`, {
      method: "POST",
      headers,
      body,
      credentials: "same-origin",
      signal: controller.signal,
    });
    if (!response.ok) {
      throw new Error(problemDetail(await response.text()) ?? `Preview failed (${response.status})`);
    }
    return (await response.json()) as ImportPreview;
  } finally {
    unregister();
  }
}

export async function downloadSiteExport(
  siteId: number,
  orgId: number,
  from: string,
  to: string,
): Promise<void> {
  const params = new URLSearchParams({ from, to });
  const headers = new Headers({ "X-Supermetry-Org": String(orgId) });
  const response = await fetch(`/api/private/sites/${siteId}/export?${params}`, {
    headers,
    credentials: "same-origin",
  });
  if (!response.ok) throw new Error(`Export failed (${response.status})`);
  const url = URL.createObjectURL(await response.blob());
  const link = document.createElement("a");
  link.href = url;
  link.download = `supermetry-export-${from}-${to}.zip`;
  link.click();
  URL.revokeObjectURL(url);
}
