import createClient, { type Middleware } from 'openapi-fetch'

import type { paths } from './api.gen'

type PrivatePaths = {
  [Path in keyof paths as Path extends `/api/v1${infer Rest}`
    ? `/api/private${Rest}`
    : Path extends `/api/private${string}`
      ? Path
      : never]: paths[Path]
} & {
  '/api/private/me': {
    get: {
      responses: {
        200: {
          content: {
            'application/json': unknown
          }
        }
      }
    }
  }
}

type ApiOptions = {
  fetch?: typeof globalThis.fetch
  onUnauthorized?: (loginURL: string) => void
}

const csrfCookie = 'panel_csrf'

function readCookie(name: string): string | undefined {
  const prefix = `${encodeURIComponent(name)}=`
  const cookie = document.cookie
    .split(';')
    .map((part) => part.trim())
    .find((part) => part.startsWith(prefix))
  if (!cookie) return undefined
  try {
    return decodeURIComponent(cookie.slice(prefix.length))
  } catch {
    return undefined
  }
}

function loginURL(): string {
  const next = `${window.location.pathname}${window.location.search}${window.location.hash}`
  return `/login?next=${encodeURIComponent(next)}`
}

export function createApi(options: ApiOptions = {}) {
  const client = createClient<PrivatePaths>({
    baseUrl: window.location.origin,
    fetch: options.fetch,
  })
  const onUnauthorized =
    options.onUnauthorized ?? ((url: string) => window.location.assign(url))
  const middleware: Middleware = {
    async onRequest({ request }) {
      if (request.method !== 'GET') {
        const token = readCookie(csrfCookie)
        if (token) {
          request.headers.set('X-CSRF-Token', token)
        }
      }
      return request
    },
    async onResponse({ response }) {
      if (response.status === 401) {
        onUnauthorized(loginURL())
      }
      return response
    },
  }
  client.use(middleware)
  return client
}

export const api = createApi()
