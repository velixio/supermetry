import { useQuery, type UseQueryResult } from '@tanstack/react-query'

import { api } from '@/lib/api'
import type { components } from '@/lib/api.gen'
import type { Filter } from '@/lib/search'

export type VitalMetric = components['schemas']['VitalMetric']
export type VitalTrendMetric = components['schemas']['VitalTrendMetric']
export type VitalMetricName = 'lcp' | 'cls' | 'inp' | 'fcp' | 'ttfb'
export type VitalsDimension = components['schemas']['VitalsDimension']
export type VitalsResponse = components['schemas']['VitalsResponse']

export type VitalsQuery = {
  siteId: number
  dateRange: { from: string; to: string }
  filters: Filter[]
  dimension: VitalsDimension
}

type Problem = { title?: string; detail?: string; status?: number }

export function useVitals(query: VitalsQuery): UseQueryResult<VitalsResponse, Problem> {
  const body = {
    site_id: query.siteId,
    date_range: { ...query.dateRange },
    filters: query.filters.map((filter) => ({
      dimension: filter.dimension,
      operator: filter.operator,
      values: [...filter.values],
    })),
    dimension: query.dimension,
  } satisfies components['schemas']['VitalsRequest']
  return useQuery<VitalsResponse, Problem>({
    queryKey: ['vitals', query.siteId, body],
    queryFn: async ({ signal }) => {
      const result = await api.POST('/api/private/vitals/query', { body, signal })
      if (result.error) throw result.error
      if (!result.data || typeof result.data === 'string') {
        throw { title: 'Empty API response' } satisfies Problem
      }
      return result.data
    },
    staleTime: 300_000,
  })
}
