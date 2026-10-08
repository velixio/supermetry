import { useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api'
import type { components } from '@/lib/api.gen'
import type { Filter } from '@/lib/search'

import type { JourneyDirection, JourneyResponse } from './types'

type Problem = components['schemas']['Problem']

export function useJourneyReport(
  siteId: number,
  dateRange: { from: string; to: string },
  filters: Filter[],
  direction: JourneyDirection,
  path: string,
  steps: number,
) {
  const anchor = path.trim()
  const body = {
    site_id: siteId,
    date_range: dateRange,
    filters,
    ...(anchor === '' ? {} : direction === 'start' ? { start_path: anchor } : { end_path: anchor }),
    steps,
    limit: 10,
  }
  return useQuery<JourneyResponse, Problem>({
    queryKey: ['journeys', body],
    queryFn: async ({ signal }) => {
      const { data, error } = await api.POST('/api/private/journeys/query', { body, signal })
      if (error) throw error
      if (!data) throw new Error('The journey response was empty.')
      return data
    },
  })
}
