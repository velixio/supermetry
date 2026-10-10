import { privateRequest } from '@/features/settings/privateApi'

export type CreatedOrganization = { id: number; name: string; slug: string }

export function createOrganization(name: string): Promise<CreatedOrganization> {
  return privateRequest<CreatedOrganization>('POST', '/api/private/orgs', { body: { name } })
}
