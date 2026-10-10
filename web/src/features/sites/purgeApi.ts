import { privateRequest, type ListResponse } from '@/features/settings/privateApi'

export type PurgeRequest = {
  id: number
  from: string
  to: string
  source: string
  country: string
  status: 'pending' | 'running' | 'settling' | 'done' | 'failed'
  sessions_deleted: number
  error?: string
  created_at: string
  completed_at?: string
}

export type PurgeInput = {
  from: string
  to: string
  source: string
  country: string
  password: string
  totp_code?: string
}

export function listPurges(siteId: number, orgId: number): Promise<ListResponse<PurgeRequest>> {
  return privateRequest('GET', `/api/private/sites/${siteId}/purge-requests`, { orgId })
}

export function createPurge(siteId: number, orgId: number, body: PurgeInput): Promise<PurgeRequest> {
  return privateRequest('POST', `/api/private/sites/${siteId}/purge-requests`, { orgId, body })
}
