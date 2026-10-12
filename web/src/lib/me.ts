import { queryOptions, useQuery } from '@tanstack/react-query'

import type { Role } from '@/components/Sidebar'
import type { TrackingPolicy } from '@/features/sites/types'
import { api } from '@/lib/api'
import { apiProblem } from '@/lib/apiProblem'

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
  user: { id: number; name: string; email: string; totp_enabled?: boolean; instance_admin?: boolean }
  memberships: Membership[]
  demo_prompt_dismissed?: boolean
  preferences?: UserPreferences
  version?: string
}

export type UserPreferences = {
  landing_page: 'overview' | 'last_site'
  sites_view: 'cards' | 'table'
  last_site_id?: number | null
  locale?: '' | 'en' | 'es' | 'de' | 'en-XA'
}

export class AuthenticationRequired extends Error {}

function isMe(value: unknown): value is Me {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Partial<Me>
  return Boolean(candidate.user && Array.isArray(candidate.memberships))
}

export async function fetchMe(): Promise<Me> {
  const { data, response } = await api.GET('/api/private/me', {})
  if (response.status === 401) throw new AuthenticationRequired()
  if (!response.ok) throw apiProblem(response, data)
  if (!isMe(data)) throw new Error('The account response was invalid.')
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
