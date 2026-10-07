import { queryOptions, useQuery } from '@tanstack/react-query'
import { createFileRoute, redirect } from '@tanstack/react-router'

import { AppShell } from '@/components/AppShell'
import {
  canCreateSites,
  navigationHref,
  organizationHref,
  visibleNavigation,
} from '@/components/Sidebar'
import type { Role } from '@/components/Sidebar'
import { api } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { en } from '@/messages/en'

export { canCreateSites, navigationHref, organizationHref, visibleNavigation }

export type Site = {
  id: number
  name: string
  slug?: string
  public_id?: string
  timezone?: string
}

export type Membership = {
  org: { id: number; name: string; slug: string }
  role: Role
  site_ids?: number[] | null
  sites: Site[]
}

export type Me = {
  user: { id: number; name: string; email: string }
  memberships: Membership[]
}

class AuthenticationRequired extends Error {}

export function loginRedirect(pathname: string, search = ''): string {
  return `/login?next=${encodeURIComponent(`${pathname}${search}`)}`
}

function isMe(value: unknown): value is Me {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Partial<Me>
  return Boolean(candidate.user && Array.isArray(candidate.memberships))
}

export async function fetchMe(): Promise<Me> {
  const { data, response } = await api.GET('/api/private/me', {})
  if (response.status === 401) throw new AuthenticationRequired()
  if (!response.ok || !isMe(data)) throw new Error(en.auth.genericError)
  return data
}

export const meQueryOptions = queryOptions({
    queryKey: ['me'],
    queryFn: fetchMe,
    staleTime: 30_000,
    retry: (count, error) => !(error instanceof AuthenticationRequired) && count < 1,
})

export function useMe() {
  return useQuery(meQueryOptions)
}

export const Route = createFileRoute('/$org')({
  beforeLoad: async ({ location, params }) => {
    try {
      const me = await queryClient.ensureQueryData(meQueryOptions)
      if (!me.memberships.some((membership) => membership.org.slug === params.org)) {
        throw new AuthenticationRequired()
      }
    } catch (error) {
      if (error instanceof AuthenticationRequired) {
        throw redirect({
          to: '/login',
          search: { next: location.href },
        })
      }
      throw error
    }
  },
  component: OrganizationLayout,
})

function OrganizationLayout() {
  const { org } = Route.useParams()
  const me = useMe()

  if (!me.data) {
    return (
      <main className="grid min-h-dvh place-items-center px-6">
        <p className="text-sm text-muted-foreground">{en.loadingDashboard}</p>
      </main>
    )
  }

  const membership = me.data.memberships.find((item) => item.org.slug === org)
  if (!membership) return null
  return <AppShell me={me.data} membership={membership} />
}
