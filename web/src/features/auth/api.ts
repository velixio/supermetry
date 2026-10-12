import { en } from '@/messages/en'

export type LoginCredentials = {
  email: string
  password: string
  totp?: string
}

export type LoginResult = { ok: boolean; type?: string }
export type SignupMode = 'open' | 'invite'
export type AuthConfig = {
  signup: SignupMode
  setup_required: boolean
  homepage_url: string
}

type Problem = { type?: string }

function problemType(value: unknown): string | undefined {
  if (!value || typeof value !== 'object') return undefined
  const type = (value as Problem).type
  return type?.split('/').pop()
}

async function request(path: string, init: RequestInit, fetcher = globalThis.fetch): Promise<Response> {
  return fetcher(new Request(new URL(path, window.location.origin), {
    ...init,
    credentials: 'same-origin',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json', ...init.headers },
  }))
}

export function createAuthApi(fetcher = globalThis.fetch) {
  return async (path: string, body: Record<string, string>): Promise<LoginResult> => {
    const response = await request(path, { method: 'POST', body: JSON.stringify(body) }, fetcher)
    if (response.ok) return { ok: true }
    let problem: unknown
    try { problem = await response.json() } catch { problem = undefined }
    return { ok: false, type: problemType(problem) }
  }
}

export function createAuthConfigApi(fetcher = globalThis.fetch) {
  return async (): Promise<AuthConfig> => {
    const response = await request('/api/private/auth/config', { method: 'GET' }, fetcher)
    if (!response.ok) throw new Error('Authentication configuration is unavailable')
    return response.json() as Promise<AuthConfig>
  }
}

export async function postPrivate(path: string, body: Record<string, string>) {
  return createAuthApi()(path, body)
}

export async function authResultMessage(operation: () => Promise<LoginResult>): Promise<string> {
  try {
    const result = await operation()
    if (result.ok) return en.auth.success
    return result.type === 'password-policy' ? en.auth.passwordPolicy : en.auth.genericError
  } catch {
    return en.auth.genericError
  }
}
