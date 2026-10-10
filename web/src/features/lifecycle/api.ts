import { privateRequest } from '@/features/settings/privateApi'

import type { components } from '@/lib/api.gen'

type Schemas = components['schemas']

export type DeletionResult = Schemas['DeletionResult']
export type DeletedSite = Schemas['DeletedSite']
export type DeletedOrganization = Schemas['DeletedOrganization']
export type Organization = Pick<Schemas['Organization'], 'id' | 'name' | 'slug'>

export type SensitiveAction = {
  password: string
  totp_code?: string
}

export function getDeletedSites(orgId: number): Promise<DeletedSite[]> {
  return privateRequest('GET', `/api/private/orgs/${orgId}/sites/deleted`, { orgId })
}

export function restoreSite(siteId: number, orgId: number): Promise<void> {
  return privateRequest('POST', `/api/private/sites/${siteId}/restore`, { orgId })
}

export function renameOrganization(orgId: number, name: string): Promise<Organization> {
  return privateRequest('PATCH', `/api/private/orgs/${orgId}`, {
    orgId,
    body: { name },
  })
}

export function deleteOrganization(orgId: number, body: SensitiveAction): Promise<DeletionResult> {
  return privateRequest('DELETE', `/api/private/orgs/${orgId}`, { orgId, body })
}

export function getDeletedOrganizations(): Promise<DeletedOrganization[]> {
  return privateRequest('GET', '/api/private/me/orgs/deleted')
}

export function restoreOrganization(orgId: number): Promise<void> {
  return privateRequest('POST', `/api/private/me/orgs/${orgId}/restore`)
}
