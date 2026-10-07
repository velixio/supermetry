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
