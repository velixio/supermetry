import { en } from '@/messages/en'
import { ApiProblem, readApiProblem } from '@/lib/apiProblem'

export type LoginCredentials = {
  email: string
  password: string
  totp?: string
}

export type LoginResult = { ok: boolean; problem?: ApiProblem; type?: string; signedIn?: boolean }
export type SignupMode = 'open' | 'invite'
export type AuthConfig = {
  signup: SignupMode
  setup_required: boolean
  homepage_url: string
  geoip_source?: string
  /** Retention preselected for new sites: 7, 30, 90, 365 or 0 (keep forever). */
  default_retention_days?: number
  /** False when the instance does not offer "keep forever" retention. */
  retention_forever?: boolean
  /** Optional help link (https or mailto); absent when not configured. */
  support_url?: string
  /** True when an operator runs the instance: the in-app instance administration is hidden. */
  managed?: boolean
}

async function request(path: string, init: RequestInit, fetcher = globalThis.fetch): Promise<Response> {
  return fetcher(new Request(new URL(path, window.location.origin), {
    ...init,
    credentials: 'same-origin',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json', ...init.headers },
  }))
}

export function createAuthApi(fetcher = globalThis.fetch) {
  return async (path: string, body: Record<string, string | boolean>): Promise<LoginResult> => {
    const response = await request(path, { method: 'POST', body: JSON.stringify(body) }, fetcher)
    if (response.ok) return { ok: true, signedIn: await responseSignedIn(response) }
    const problem = await readApiProblem(response)
    return { ok: false, problem, type: problem.type }
  }
}

// A 200 with {"authenticated":true} means the server also opened a session
// (password reset with sign_in); a 204 or any other body means it did not.
async function responseSignedIn(response: Response): Promise<boolean> {
  if (response.status !== 200) return false
  try {
    const body = (await response.json()) as { authenticated?: boolean }
    return body.authenticated === true
  } catch {
    return false
  }
}

export function createAuthConfigApi(fetcher = globalThis.fetch) {
  return async (): Promise<AuthConfig> => {
    const response = await request('/api/private/auth/config', { method: 'GET' }, fetcher)
    if (!response.ok) throw new Error('Authentication configuration is unavailable')
    return response.json() as Promise<AuthConfig>
  }
}

export async function postPrivate(path: string, body: Record<string, string | boolean>) {
  return createAuthApi()(path, body)
}

export async function authResultMessage(operation: () => Promise<LoginResult>): Promise<string> {
  try {
    const result = await operation()
    if (result.ok) return en.auth.success
    return result.type?.endsWith('password-policy') ? en.auth.passwordPolicy : authProblemMessage(result.problem)
  } catch (error) {
    return error instanceof ApiProblem ? authProblemMessage(error) : en.auth.networkError
  }
}

export function authProblemMessage(problem?: ApiProblem): string {
  const type = problem?.type?.split('/').pop()?.toLowerCase() ?? ''
  const description = `${problem?.title ?? ''} ${problem?.detail ?? ''}`.toLowerCase()
  if (type.includes('password-policy')) return en.auth.passwordPolicy
  if (/(setup|invite|reset).*(invalid|expired|used)|(invalid|expired|used).*(setup|invite|reset)/.test(type) || /(?:invitation|setup|reset) (?:is )?invalid|token is invalid|token is expired/.test(description)) return en.auth.invalidToken
  if (problem?.status === 429) return en.auth.rateLimited
  if (problem?.status === 403 && (/origin/.test(type) || /origin/.test(description))) return en.auth.originMismatch
  if (problem?.status && problem.status >= 500) return en.auth.serverError
  if (problem?.status === 401 || /invalid-credentials|invalid-password|authentication/.test(type)) return en.auth.invalidCredentials
  return en.auth.genericError
}
