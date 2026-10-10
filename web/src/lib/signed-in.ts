// signedInRedirect checks for an existing session without going through the
// API client: a 401 there would trigger the session-expired flow, which is
// wrong on the login and signup pages, where being signed out is normal.
export async function signedInRedirect(
  target: string,
  fetchMe: typeof fetch = (...args) => globalThis.fetch(...args),
): Promise<string | null> {
  try {
    const response = await fetchMe('/api/private/me', {
      credentials: 'same-origin',
      headers: { Accept: 'application/json' },
    })
    if (!response.ok) return null
    const body = (await response.json()) as { user?: unknown; memberships?: unknown }
    return body && body.user && Array.isArray(body.memberships) ? target : null
  } catch {
    return null
  }
}
