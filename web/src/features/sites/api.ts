import { privateRequest } from '@/features/settings/privateApi'
import { api } from '@/lib/api'
import { apiProblem } from '@/lib/apiProblem'

import type { ManagedSite, SiteInput, SiteUpdate } from './types'
import type { DeletionResult } from '@/features/lifecycle/api'

export type OverviewPeriod = '24h' | '7d' | '30d'

export type OverviewSite = {
  id: number
  org_id: number
  public_id: string
  name: string
  domain: string
  timezone: string
  icon_url?: string
  organization: { id: number; name: string; slug: string }
  role: 'owner' | 'admin' | 'editor' | 'viewer'
  created_at: string
  visitors: number
  previous_visitors: number
  change_percent: number | null
  sparkline: Array<{ start: string; visitors: number }>
  live_visitors: number
  last_event_at: string | null
  install_status: 'waiting' | 'receiving' | 'quiet'
  tracking_snippet: string
}

export type SitesOverviewResponse = {
  data: OverviewSite[]
  next_cursor?: string | null
}

export function listSitesOverview(period: OverviewPeriod): Promise<SitesOverviewResponse> {
  return privateRequest('GET', `/api/private/sites/overview?period=${period}&limit=200`)
}

export function createSite(input: SiteInput, orgId: number): Promise<ManagedSite> {
  return privateRequest<ManagedSite>('POST', '/api/private/sites', {
    orgId,
    body: input,
  })
}

export async function deleteSite(siteId: number, orgId: number): Promise<DeletionResult> {
  return (await privateRequest<DeletionResult | undefined>('DELETE', `/api/private/sites/${siteId}`, { orgId })) ?? {
    scheduled: false,
    purge_at: null,
  }
}

export class SiteRequestError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message)
    this.name = 'SiteRequestError'
  }
}

export async function getSite(siteId: number, orgId: number): Promise<ManagedSite> {
  try {
    return await privateRequest<ManagedSite>('GET', `/api/private/sites/${siteId}`, { orgId })
  } catch (error) {
    if (error instanceof Error) {
      const statusMatch = error.message.match(/\((\d{3})\)$/)
      const status = statusMatch ? Number(statusMatch[1]) : /not found/i.test(error.message) ? 404 : undefined
      if (status !== undefined) throw new SiteRequestError(error.message, status)
    }
    throw error
  }
}

export function updateSite(
  siteId: number,
  orgId: number,
  input: SiteUpdate,
): Promise<ManagedSite> {
  return privateRequest<ManagedSite>('PATCH', `/api/private/sites/${siteId}`, {
    orgId,
    body: input,
  })
}

export class SiteIconRefetchError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message)
    this.name = 'SiteIconRefetchError'
  }
}

export async function refetchSiteIcon(siteId: number, orgId: number): Promise<ManagedSite> {
  const { data, error, response } = await api.POST('/api/private/sites/{site}/icon/refetch', {
    params: {
      path: { site: siteId },
      header: { 'X-Supermetry-Org': String(orgId) },
    },
  })
  if (!response.ok || !data) {
    throw new SiteIconRefetchError(
      apiProblem(response, error).message,
      response.status,
    )
  }
  return data
}
