import { keepPreviousData, useQuery } from '@tanstack/react-query'

import { privateRequest } from '@/features/settings/privateApi'

import type { ObsMetricInfo, ObsMetricQuery, ObsMetricResult } from './metricQuery'

const base = (siteId: number) => `/api/private/sites/${siteId}/obs/metrics`

/** Metric names of an app project with kind, unit, series count and attribute keys. */
export function useObsMetrics(siteId: number, enabled = true) {
  return useQuery({
    queryKey: ['obs-metrics', siteId],
    queryFn: async () => (await privateRequest<{ metrics: ObsMetricInfo[] }>('GET', base(siteId))).metrics,
    enabled,
    staleTime: 60_000,
  })
}

/** One metric query; idle while `query` is null. */
export function useObsMetricQuery(siteId: number, query: ObsMetricQuery | null) {
  return useQuery({
    queryKey: ['obs-metric-query', siteId, query],
    queryFn: () => privateRequest<ObsMetricResult>('POST', `${base(siteId)}/query`, { body: query }),
    enabled: query !== null,
    placeholderData: keepPreviousData,
    staleTime: 30_000,
  })
}
