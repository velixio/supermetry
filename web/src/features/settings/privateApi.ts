import { registerPrivateTask } from "@/lib/session-lifecycle";
import { api } from "@/lib/api";

type Method = "GET" | "POST" | "PATCH" | "DELETE";
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
    ? { "X-Panel-Org": String(options.orgId) }
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

export type ImportSource = "plausible" | "umami";

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
      fail(new Error(`Upload failed (${request.status})`));
    };
    const error = () => fail(new Error("Upload failed"));
    const abort = () => fail(new DOMException("Session changed", "AbortError"));
    try {
      unregister = register(() => {
        abort();
        request.abort();
      });
      request.open("POST", `/api/private/sites/${siteId}/imports`);
      request.setRequestHeader("X-Panel-Org", String(orgId));
      const csrf = cookie("panel_csrf");
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

export async function downloadSiteExport(
  siteId: number,
  orgId: number,
  from: string,
  to: string,
): Promise<void> {
  const params = new URLSearchParams({ from, to });
  const headers = new Headers({ "X-Panel-Org": String(orgId) });
  const response = await fetch(`/api/private/sites/${siteId}/export?${params}`, {
    headers,
    credentials: "same-origin",
  });
  if (!response.ok) throw new Error(`Export failed (${response.status})`);
  const url = URL.createObjectURL(await response.blob());
  const link = document.createElement("a");
  link.href = url;
  link.download = `panel-export-${from}-${to}.zip`;
  link.click();
  URL.revokeObjectURL(url);
}
