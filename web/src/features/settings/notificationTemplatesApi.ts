import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import type { components } from '@/lib/api.gen'
import { privateRequest } from './privateApi'
import type { ChannelKind } from './notificationApi'

export type NotificationTemplate = components['schemas']['NotificationTemplate']
export type TemplateInput = components['schemas']['NotificationTemplateInput']
export type RenderedNotification = components['schemas']['RenderedNotification']
export type NotificationSettings = components['schemas']['NotificationSettings']
export type TemplateList = { data: NotificationTemplate[]; settings: NotificationSettings; variables: string[] }

const templatesKey = (orgId: number) => ['orgs', orgId, 'notification-templates'] as const

export function useNotificationTemplates(orgId: number) {
  return useQuery({ queryKey: templatesKey(orgId), queryFn: () => privateRequest<TemplateList>('GET', `/api/private/orgs/${orgId}/notification-templates`, { orgId }) })
}

export function useSaveNotificationTemplate(orgId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ kind, input }: { kind: ChannelKind; input: TemplateInput }) => privateRequest<NotificationTemplate>('PUT', `/api/private/orgs/${orgId}/notification-templates/${kind}`, { orgId, body: input }),
    onSuccess: () => client.invalidateQueries({ queryKey: templatesKey(orgId) }),
  })
}

export function useResetNotificationTemplate(orgId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (kind: ChannelKind) => privateRequest<NotificationTemplate>('DELETE', `/api/private/orgs/${orgId}/notification-templates/${kind}`, { orgId }),
    onSuccess: () => client.invalidateQueries({ queryKey: templatesKey(orgId) }),
  })
}

export function previewNotificationTemplate(orgId: number, kind: ChannelKind, input: TemplateInput) {
  return privateRequest<RenderedNotification>('POST', `/api/private/orgs/${orgId}/notification-templates/${kind}/preview`, { orgId, body: input })
}

export function useSaveNotificationSettings(orgId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: NotificationSettings) => privateRequest<NotificationSettings>('PUT', `/api/private/orgs/${orgId}/notification-settings`, { orgId, body: input }),
    onSuccess: () => client.invalidateQueries({ queryKey: templatesKey(orgId) }),
  })
}
