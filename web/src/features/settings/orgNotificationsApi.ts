import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { privateRequest } from './privateApi'

export type OrgNotificationEventKind = { kind: string; label: string; description: string }

export type OrgNotificationChannel = {
  id: number
  kind: 'telegram'
  enabled: boolean
  chat_id: string
  token_set: boolean
  events: Record<string, boolean>
  created_at: string
  updated_at: string
  last_ok_at: string | null
  last_error: string
  consecutive_failures: number
}

export type OrgNotificationList = {
  data: OrgNotificationChannel[]
  event_kinds: OrgNotificationEventKind[]
}

export type OrgTelegramInput = {
  token?: string
  chat_id: string
  enabled: boolean
  events: Record<string, boolean>
}

export type OrgNotificationTestResult = { ok: boolean; error?: string }

export const orgNotificationKeys = {
  list: (orgId: number) => ['orgs', orgId, 'org-notifications'] as const,
}

const base = (orgId: number) => `/api/private/orgs/${orgId}/notifications`

export function useOrgNotifications(orgId: number) {
  return useQuery({
    queryKey: orgNotificationKeys.list(orgId),
    queryFn: () => privateRequest<OrgNotificationList>('GET', base(orgId), { orgId }),
  })
}

export function useSaveOrgTelegram(orgId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: OrgTelegramInput) =>
      privateRequest<OrgNotificationChannel>('PUT', `${base(orgId)}/telegram`, { orgId, body: input }),
    onSuccess: () => client.invalidateQueries({ queryKey: orgNotificationKeys.list(orgId) }),
  })
}

export function useDeleteOrgTelegram(orgId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => privateRequest<void>('DELETE', `${base(orgId)}/telegram`, { orgId }),
    onSuccess: () => client.invalidateQueries({ queryKey: orgNotificationKeys.list(orgId) }),
  })
}

export function useTestOrgTelegram(orgId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: () =>
      privateRequest<OrgNotificationTestResult>('POST', `${base(orgId)}/telegram/test`, { orgId }),
    onSettled: () => client.invalidateQueries({ queryKey: orgNotificationKeys.list(orgId) }),
  })
}
