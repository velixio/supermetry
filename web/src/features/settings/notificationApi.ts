import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { privateRequest, type ListResponse } from './privateApi'

export type ChannelKind = 'email' | 'webhook' | 'slack' | 'discord'

export type NotificationChannel = {
  id: number
  org_id: number
  kind: ChannelKind
  name: string
  config: { recipients?: string[]; configured?: boolean }
  created_at: string
}

export type ChannelInput = {
  kind: ChannelKind
  name: string
  config: { recipients?: string[]; url?: string; secret?: string }
}

export const notificationKeys = {
  all: ['orgs'] as const,
  org: (orgId: number) => [...notificationKeys.all, orgId] as const,
  channels: (orgId: number) => [...notificationKeys.org(orgId), 'channels'] as const,
}

export function useChannels(orgId: number) {
  return useQuery({
    queryKey: notificationKeys.channels(orgId),
    queryFn: () => privateRequest<ListResponse<NotificationChannel>>(
      'GET', `/api/private/orgs/${orgId}/channels`, { orgId },
    ),
  })
}

export function useSaveChannel(orgId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, input }: { id?: number; input: ChannelInput }) => id === undefined
      ? privateRequest<NotificationChannel>('POST', `/api/private/orgs/${orgId}/channels`, { orgId, body: input })
      : privateRequest<NotificationChannel>('PATCH', `/api/private/orgs/${orgId}/channels/${id}`, { orgId, body: input }),
    onSuccess: () => client.invalidateQueries({ queryKey: notificationKeys.channels(orgId) }),
  })
}

export function useDeleteChannel(orgId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => privateRequest<void>('DELETE', `/api/private/orgs/${orgId}/channels/${id}`, { orgId }),
    onSuccess: () => client.invalidateQueries({ queryKey: notificationKeys.channels(orgId) }),
  })
}

export function useTestChannel(orgId: number) {
  return useMutation({
    mutationFn: (id: number) => privateRequest<void>('POST', `/api/private/orgs/${orgId}/channels/${id}/test`, { orgId }),
  })
}
