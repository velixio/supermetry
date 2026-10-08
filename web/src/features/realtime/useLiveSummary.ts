import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api'
import type { components } from '@/lib/api.gen'

type LiveSummary = components['schemas']['RealtimeSummary']

class LiveSummaryError extends Error {
  constructor(readonly status: number, message: string) {
    super(message)
    this.name = 'LiveSummaryError'
  }
}

export const liveSummaryKey = (siteId: number) => ['realtime-summary', siteId] as const

export function useLiveSummary(siteId: number) {
  const [visible, setVisible] = useState(() => !document.hidden)

  useEffect(() => {
    const onVisibilityChange = () => setVisible(!document.hidden)
    document.addEventListener('visibilitychange', onVisibilityChange)
    return () => document.removeEventListener('visibilitychange', onVisibilityChange)
  }, [])

  return useQuery<LiveSummary, LiveSummaryError>({
    queryKey: liveSummaryKey(siteId),
    queryFn: async () => {
      const result = await api.GET('/api/private/realtime/summary', {
        params: { query: { site_id: siteId } },
      })
      const status = result.response.status
      if (result.error) {
        throw new LiveSummaryError(status, result.error.detail ?? result.error.title)
      }
      if (!result.data) {
        throw new LiveSummaryError(status, 'The realtime summary response was empty.')
      }
      return result.data
    },
    staleTime: 15_000,
    refetchInterval: visible ? 30_000 : false,
    refetchIntervalInBackground: false,
    retry: (failureCount, error) => error.status !== 403 && failureCount < 1,
  })
}
