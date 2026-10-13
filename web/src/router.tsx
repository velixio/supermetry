import { createRouter } from '@tanstack/react-router'

import { stringifySearch } from '@/lib/search'
import { RoutePending } from './components/RoutePending'
import { routeTree } from './routeTree.gen'

function parseValue(value: string): unknown {
  if (value === 'true') return true
  if (value === 'false') return false
  if (value.startsWith('{') || value.startsWith('[')) {
    try {
      return JSON.parse(value) as unknown
    } catch {
      return value
    }
  }
  return value
}

export function parseSearch(search: string): Record<string, unknown> {
  const params = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search)
  const result: Record<string, unknown> = {}
  for (const key of new Set(params.keys())) {
    const values = params.getAll(key).map(parseValue)
    result[key] = key === 'f' || key === 'cursors' || values.length > 1 ? values : values[0]
  }
  return result
}

export { stringifySearch }

export const router = createRouter({
  routeTree,
  defaultPreload: 'intent',
  defaultPreloadDelay: 50,
  defaultPreloadStaleTime: 0,
  defaultPendingComponent: RoutePending,
  defaultPendingMs: 180,
  defaultPendingMinMs: 200,
  scrollRestoration: true,
  parseSearch,
  stringifySearch,
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
