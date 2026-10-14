export type ApiProblemBody = { type?: string; title?: string; status?: number; detail?: string }

export class ApiProblem extends Error {
  readonly status: number
  readonly type?: string
  readonly title?: string
  readonly detail?: string

  constructor(status: number, body: ApiProblemBody = {}, fallback?: string) {
    super(body.detail || body.title || fallback || `Request failed (${status})`)
    this.name = 'ApiProblem'
    this.status = status
    this.type = body.type
    this.title = body.title
    this.detail = body.detail
  }
}

export function apiProblem(response: Response, body?: unknown, fallback?: string): ApiProblem {
  const value = body && typeof body === 'object' ? body as ApiProblemBody : {}
  return new ApiProblem(response.status, value, fallback)
}

export async function readApiProblem(response: Response, fallback?: string): Promise<ApiProblem> {
  let body: unknown
  try { body = await response.clone().json() } catch { body = undefined }
  return apiProblem(response, body, fallback)
}
