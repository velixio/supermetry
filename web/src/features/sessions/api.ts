import { useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api'
import type { components } from '@/lib/api.gen'
import type { Filter } from '@/lib/search'

import type { SessionDetail, SessionsResponse } from './types'

type Problem = components['schemas']['Problem']

export const sessionKeys = {
  all: (siteId: number) => ['sessions', siteId] as const,
  list: (siteId: number, dateRange: { from: string; to: string }, filters: Filter[], cursor?: string, hasReplay = false) =>
    [...sessionKeys.all(siteId), 'list', { dateRange, filters, cursor, hasReplay }] as const,
  detail: (siteId: number, sessionId?: string) =>
    [...sessionKeys.all(siteId), 'detail', sessionId] as const,
}

export function useSessions(
  siteId: number,
  dateRange: { from: string; to: string },
  filters: Filter[],
  cursor?: string,
  hasReplay = false,
) {
  return useQuery<SessionsResponse, Problem>({
    queryKey: sessionKeys.list(siteId, dateRange, filters, cursor, hasReplay),
    queryFn: async ({ signal }) => {
      const { data, error } = await api.GET('/api/private/sites/{site_id}/sessions', {
        params: {
          path: { site_id: siteId },
          query: {
            from: dateRange.from,
            to: dateRange.to,
            filter: filters.map((filter) => `${filter.dimension}:${filter.operator}:${filter.values.join(',')}`),
            cursor,
            limit: 50,
            has_replay: hasReplay ? 1 : undefined,
          },
        },
        signal,
      })
      if (error) throw error
      if (!data) throw new Error('The sessions response was empty.')
      return data
    },
  })
}

export function useSession(siteId: number, sessionId?: string) {
  return useQuery<SessionDetail, Problem>({
    queryKey: sessionKeys.detail(siteId, sessionId),
    enabled: Boolean(sessionId),
    queryFn: async ({ signal }) => {
      const { data, error } = await api.GET('/api/private/sessions/{session_id}', {
        params: {
          path: { session_id: sessionId! },
          query: { site_id: siteId },
        },
        signal,
      })
      if (error) throw error
      if (!data) throw new Error('The session response was empty.')
      return data
    },
  })
}
