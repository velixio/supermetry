import { useInfiniteQuery, useQuery } from '@tanstack/react-query'

import { privateRequest } from '@/features/settings/privateApi'
import type { components } from '@/lib/api.gen'

export type ObsLog = components['schemas']['ObsLog']
export type ObsLogPage = components['schemas']['ObsLogPage']

const base = (siteId: number) => `/api/private/sites/${siteId}/obs`

/** One page of log search results; `next_cursor` drives "Load more". */
export function useObsLogSearch(siteId: number, q: string, from: string, to: string) {
  return useInfiniteQuery({
    queryKey: ['obs-logs', siteId, q, from, to],
    initialPageParam: '',
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ from, to, limit: '100' })
      if (q.trim()) params.set('q', q.trim())
      if (pageParam) params.set('cursor', pageParam)
      return privateRequest<ObsLogPage>('GET', `${base(siteId)}/logs?${params}`)
    },
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    retry: false,
  })
}

/** The logs of one trace, oldest first. */
export function useTraceLogs(siteId: number, traceId: string, around?: string) {
  return useQuery({
    queryKey: ['obs-trace-logs', siteId, traceId, around ?? ''],
    queryFn: async () => {
      const params = new URLSearchParams({ limit: '200' })
      if (around) params.set('around', around)
      return (await privateRequest<{ data: ObsLog[] }>('GET', `${base(siteId)}/traces/${encodeURIComponent(traceId)}/logs?${params}`)).data
    },
  })
}
