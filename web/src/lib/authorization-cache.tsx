import { useLayoutEffect, useState, type ReactNode } from 'react'
import {
  useQueryClient,
  type Query,
  type QueryClient,
} from '@tanstack/react-query'
import type { Membership } from '@/lib/me'

export function authorizationScope(
  userId: number,
  membership: Membership | undefined,
): string {
  // Outside an organization (account, admin) no organization grant applies.
  if (!membership) return JSON.stringify([userId, null])
  return JSON.stringify([
    userId,
    membership.org.id,
    membership.role,
    membership.site_ids == null
      ? null
      : [...membership.site_ids].sort((a, b) => a - b),
    membership.sites.map((site) => site.id).sort((a, b) => a - b),
    [...(membership.effective_capabilities ?? [])].sort(),
  ])
}

function isSensitiveQuery(query: Query): boolean {
  const [kind, , resource] = query.queryKey
  return (
    ['profiles', 'replay', 'sessions', 'retention'].includes(String(kind)) ||
    (kind === 'sites' && resource === 'events')
  )
}

export function clearSensitiveQueries(client: QueryClient): void {
  // Cancel before removing, so an old authorized response cannot refill a cache
  // after a role change, organization switch, or sign-out.
  void client.cancelQueries({ predicate: isSensitiveQuery })
  client.removeQueries({ predicate: isSensitiveQuery })
}

export function AuthorizationCacheBoundary({
  scope,
  children,
}: {
  scope: string
  children: ReactNode
}) {
  const client = useQueryClient()
  const [readyScope, setReadyScope] = useState(scope)

  useLayoutEffect(() => {
    if (readyScope === scope) return
    clearSensitiveQueries(client)
    setReadyScope(scope)
  }, [client, readyScope, scope])

  useLayoutEffect(() => () => clearSensitiveQueries(client), [client])

  // Do not mount a route with the previous authorization context's cached data.
  // The shell stays mounted; only its content pauses until the cache is cleared.
  return readyScope === scope ? children : null
}
