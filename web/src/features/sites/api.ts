import { privateRequest } from '@/features/settings/privateApi'

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
