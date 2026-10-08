import { privateRequest } from '@/features/settings/privateApi'
import { api } from '@/lib/api'

import type { ManagedSite, SiteInput } from './types'

export function createSite(input: SiteInput, orgId: number): Promise<ManagedSite> {
  return privateRequest<ManagedSite>('POST', '/api/private/sites', {
    orgId,
    body: input,
  })
}

export function deleteSite(siteId: number, orgId: number): Promise<void> {
  return privateRequest<void>('DELETE', `/api/private/sites/${siteId}`, { orgId })
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
      header: { 'X-Panel-Org': String(orgId) },
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
