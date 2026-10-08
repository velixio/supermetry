import { useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api'
import type { components } from '@/lib/api.gen'
import type { Filter } from '@/lib/search'

import type { RetentionIdentity, RetentionInterval, RetentionResponse } from './types'

type Problem = components['schemas']['Problem']

export function useRetentionReport(
  siteId: number,
  dateRange: { from: string; to: string },
  filters: Filter[],
  interval: RetentionInterval,
  identity: RetentionIdentity,
) {
  const body = { site_id: siteId, date_range: dateRange, filters, interval, identity }
  return useQuery<RetentionResponse, Problem>({
    queryKey: ['retention', body],
    queryFn: async ({ signal }) => {
      const { data, error } = await api.POST('/api/private/retention/query', { body, signal })
      if (error) throw error
      if (!data) throw new Error('The retention response was empty.')
      return data as RetentionResponse
    },
  })
}
