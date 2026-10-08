import { useQuery } from '@tanstack/react-query'

import { privateRequest } from '@/features/settings/privateApi'
import { api } from '@/lib/api'
import type { components } from '@/lib/api.gen'

import type { HeatmapDevice, HeatmapResponse, HeatmapSite } from './types'

type DateRange = { from: string; to: string }
type Problem = components['schemas']['Problem']

export function useHeatmapSite(siteId: number, orgId: number) {
  return useQuery<HeatmapSite, Problem>({
    queryKey: ['site', siteId],
    queryFn: () => privateRequest<HeatmapSite>('GET', `/api/private/sites/${siteId}`, { orgId }),
  })
}

export function useHeatmapReport(
  siteId: number,
  dateRange: DateRange,
  path: string,
  device: HeatmapDevice,
  enabled: boolean,
) {
  const body = {
    site_id: siteId,
    date_range: dateRange,
    path,
    device,
  }
  return useQuery<HeatmapResponse, Problem>({
    queryKey: ['heatmap', body],
    queryFn: async ({ signal }) => {
      const result = await api.POST('/api/private/heatmaps/query', { body, signal })
      if (result.error) throw result.error
      if (!result.data) throw new Error('The heatmap response was empty.')
      return result.data
    },
    enabled: enabled && path !== '',
    staleTime: 300_000,
  })
}
