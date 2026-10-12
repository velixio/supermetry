import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import type { components } from '@/lib/api.gen'
import { privateRequest, type ListResponse } from './privateApi'

export type NotificationDelivery = components['schemas']['NotificationDelivery']
export type DeliveryStatus = components['schemas']['DeliveryStatus']
export type HistoryFilters = { status?: DeliveryStatus; channel?: string; alertId?: number }

export const historyKeys = {
  site: (siteId: number) => ['sites', siteId, 'notification-history'] as const,
  list: (siteId: number, filters: HistoryFilters) => [...historyKeys.site(siteId), filters] as const,
}

export function useNotificationHistory(siteId: number, orgId: number, filters: HistoryFilters) {
  const search = new URLSearchParams()
  if (filters.status) search.set('status', filters.status)
  if (filters.channel) search.set('channel', filters.channel)
  if (filters.alertId) search.set('alert_id', String(filters.alertId))
  const suffix = search.size ? `?${search}` : ''
  return useQuery({
    queryKey: historyKeys.list(siteId, filters),
    queryFn: () => privateRequest<ListResponse<NotificationDelivery>>('GET', `/api/private/sites/${siteId}/notification-history${suffix}`, { orgId }),
  })
}

export function useRetryNotification(siteId: number, orgId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (deliveryId: number) => privateRequest<NotificationDelivery>('POST', `/api/private/sites/${siteId}/notification-history/${deliveryId}/retry`, { orgId }),
    onSuccess: () => client.invalidateQueries({ queryKey: historyKeys.site(siteId) }),
  })
}
