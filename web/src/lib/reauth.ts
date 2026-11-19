import { ApiProblem, readApiProblem } from './apiProblem'

/** Endpoint of the step-up check. Single place to switch if the backend path changes. */
export const REAUTH_ENDPOINT = '/api/private/auth/reauth'

export type ReauthValues = { password: string; totp_code?: string }
export type ReauthSubmit = (values: ReauthValues) => Promise<void>

/** True for an API error that asks the user to confirm their password again. */
export function isReauthRequired(error: unknown): boolean {
  if (error instanceof ApiProblem) return error.code === 'reauth_required' || /reauth_required$/.test(error.type ?? '')
  if (error && typeof error === 'object') {
    const e = error as { code?: unknown; error?: { code?: unknown } }
    return e.code === 'reauth_required' || e.error?.code === 'reauth_required'
  }
  return false
}

/** True when the reauth call itself says a TOTP code is needed. */
export function isTotpRequired(error: unknown): boolean {
  return error instanceof ApiProblem && error.code === 'totp_required'
}

function csrfToken(): string | undefined {
  const match = document.cookie.split(';').map((p) => p.trim()).find((p) => p.startsWith('supermetry_csrf='))
  if (!match) return undefined
  try { return decodeURIComponent(match.slice('supermetry_csrf='.length)) } catch { return undefined }
}

/** POST /api/private/auth/reauth {password, totp_code?}. Throws ApiProblem on a non-2xx reply. */
export const postReauth: ReauthSubmit = async (values) => {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  const token = csrfToken()
  if (token) headers['X-CSRF-Token'] = token
  const response = await fetch(REAUTH_ENDPOINT, {
    method: 'POST',
    credentials: 'same-origin',
    headers,
    body: JSON.stringify(values),
  })
  if (!response.ok) throw await readApiProblem(response)
}
