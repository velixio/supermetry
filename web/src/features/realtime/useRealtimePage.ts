import { useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api'
import type { components } from '@/lib/api.gen'

import { liveSummaryKey } from './useLiveSummary'

type Problem = components['schemas']['Problem']
type Realtime = components['schemas']['RealtimeResponse']
type RealtimeApi = {
  GET: (
    path: '/api/private/realtime',
    options: { params: { query: { site_id: number } } },
  ) => Promise<{ data?: Realtime; error?: Problem; response: Response }>
}

const realtimeApi = api as unknown as RealtimeApi

export function useRealtimePage(siteId: number) {
  const queryClient = useQueryClient()

  return useQuery<Realtime, Problem>({
    queryKey: ['realtime', siteId],
    queryFn: async () => {
      const { data, error } = await realtimeApi.GET('/api/private/realtime', {
        params: { query: { site_id: siteId } },
      })
      if (error) throw error
      if (!data) throw new Error('The realtime response was empty.')
      queryClient.setQueryData(liveSummaryKey(siteId), {
        active_visitors: data.active_visitors,
        visitors_last_30m: data.visitors_last_30m,
      })
      return data
    },
    staleTime: 5_000,
    refetchInterval: () => document.hidden ? false : 5_000,
    refetchIntervalInBackground: false,
  })
}
