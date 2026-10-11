import { queryOptions, useQuery } from '@tanstack/react-query'
import { createFileRoute, Link, redirect } from '@tanstack/react-router'

import type { TrackingPolicy } from '@/features/sites/types'

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
  tracking_policy?: TrackingPolicy
  id: number
  name: string
  slug?: string
  public_id?: string
  timezone?: string
  retention_days?: number
  profiles_enabled?: boolean
  icon_url?: string
  allowed_hosts?: string[]
  revenue_currency?: string
  revenue_currency_state?: 'unconfigured' | 'configured' | 'conflict'
  is_demo?: boolean
  demo_ready?: boolean
}

export type Membership = {
  org: { id: number; name: string; slug: string }
  role: Role
  site_ids?: number[] | null
  capabilities?: string[]
  billing_admin?: boolean
  effective_capabilities?: string[]
  sites: Site[]
}

export type Me = {
  user: { id: number; name: string; email: string; totp_enabled?: boolean }
  memberships: Membership[]
  demo_prompt_dismissed?: boolean
  version?: string
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

export const Route = createFileRoute('/app/$org')({
  beforeLoad: async ({ location }) => {
    try {
      await queryClient.ensureQueryData(meQueryOptions)
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
  if (!membership) return <OrganizationNotFound me={me.data} />
  return <AppShell me={me.data} membership={membership} />
}

function OrganizationNotFound({ me }: { me: Me }) {
  return (
    <main className="grid min-h-dvh place-items-center px-6 py-12">
      <section className="flex w-full max-w-lg flex-col gap-5 border-l-2 border-accent pl-5">
        <div className="flex flex-col gap-2">
          <h1 className="text-2xl font-semibold tracking-tight">Not found</h1>
          <p className="text-sm text-muted-foreground">
            We could not find that organisation
          </p>
        </div>
        {me.memberships.length > 0 ? (
          <nav aria-label="Available organisations">
            <p className="mb-2 text-sm font-medium">Available organisations</p>
            <ul className="flex flex-col gap-2">
              {me.memberships.map((membership) => (
                <li key={membership.org.id}>
                  <Link
                    className="text-sm text-primary underline-offset-4 hover:underline"
                    to={organizationHref(membership)}
                  >
                    {membership.org.name}
                  </Link>
                </li>
              ))}
            </ul>
          </nav>
        ) : null}
        <Link
          className="text-sm font-medium text-primary underline-offset-4 hover:underline"
          to="/app"
        >
          Dashboard
        </Link>
      </section>
    </main>
  )
}
