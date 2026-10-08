import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { privateRequest, type ListResponse } from './privateApi'

export type AlertKind = 'traffic_spike' | 'traffic_drop' | 'goal_reached'
export type AlertInput = {
  kind: AlertKind
  threshold: number
  goal_id?: number
  channel_ids: number[]
  enabled: boolean
}
export type AlertRule = AlertInput & {
  id: number
  site_id: number
  last_fired_at?: string
  created_at: string
}

export const alertKeys = {
  all: ['sites'] as const,
  site: (siteId: number) => [...alertKeys.all, siteId] as const,
  alerts: (siteId: number) => [...alertKeys.site(siteId), 'alerts'] as const,
}

export function useAlerts(siteId: number, orgId: number) {
  return useQuery({
    queryKey: alertKeys.alerts(siteId),
    queryFn: () => privateRequest<ListResponse<AlertRule>>('GET', `/api/private/sites/${siteId}/alerts`, { orgId }),
  })
}

export function useSaveAlert(siteId: number, orgId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, input }: { id?: number; input: AlertInput }) => id === undefined
      ? privateRequest<AlertRule>('POST', `/api/private/sites/${siteId}/alerts`, { orgId, body: input })
      : privateRequest<AlertRule>('PATCH', `/api/private/sites/${siteId}/alerts/${id}`, { orgId, body: input }),
    onSuccess: () => client.invalidateQueries({ queryKey: alertKeys.alerts(siteId) }),
  })
}

export function useDeleteAlert(siteId: number, orgId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => privateRequest<void>('DELETE', `/api/private/sites/${siteId}/alerts/${id}`, { orgId }),
    onSuccess: () => client.invalidateQueries({ queryKey: alertKeys.alerts(siteId) }),
  })
}
