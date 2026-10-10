import { privateRequest } from '@/features/settings/privateApi'
import { api } from '@/lib/api'

import type { ManagedSite, SiteInput, SiteUpdate } from './types'

export function createSite(input: SiteInput, orgId: number): Promise<ManagedSite> {
  return privateRequest<ManagedSite>('POST', '/api/private/sites', {
    orgId,
    body: input,
  })
}

export function deleteSite(siteId: number, orgId: number): Promise<void> {
  return privateRequest<void>('DELETE', `/api/private/sites/${siteId}`, { orgId })
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
    const problem = error as { detail?: string } | undefined
    throw new SiteIconRefetchError(
      problem?.detail ?? `Request failed (${response.status})`,
      response.status,
    )
  }
  return data
}
