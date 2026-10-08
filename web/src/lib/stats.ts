import { useQuery, type UseQueryResult } from '@tanstack/react-query'

import { api } from './api'
import type { components } from './api.gen'

type DateRange = components['schemas']['DateRange']
type Metric = components['schemas']['Metric']
type Dimension = string
type Filter = components['schemas']['Filter']
type OrderBy = components['schemas']['OrderBy']
type QueryResponse = components['schemas']['QueryResponse']
type Realtime = components['schemas']['RealtimeResponse']
type Problem = components['schemas']['Problem']
type Compare = 'off' | 'previous_period' | 'previous_year'

export type StatsQuery = {
  siteId: number
  dateRange: DateRange
  metrics: Metric[]
  dimensions?: Dimension[]
  filters: Filter[]
  orderBy?: OrderBy[]
  limit?: number
  cursor?: string
  compare?: Compare
  timezone?: string
  live?: boolean
  includeImported?: boolean
}

type QueryBody = components['schemas']['QueryRequest']

function normalizedBody(query: StatsQuery): QueryBody {
  return {
    site_id: query.siteId,
    date_range: { ...query.dateRange },
    metrics: [...query.metrics],
    ...(query.dimensions ? { dimensions: [...query.dimensions] } : {}),
    filters: query.filters.map((filter) => ({
      dimension: filter.dimension,
      operator: filter.operator,
      values: [...filter.values],
    })),
    ...(query.orderBy
      ? { order_by: query.orderBy.map((order) => ({ ...order })) }
      : {}),
    ...(query.limit === undefined ? {} : { limit: query.limit }),
    ...(query.cursor === undefined ? {} : { cursor: query.cursor }),
    ...(query.compare && query.compare !== 'off'
      ? { compare: query.compare }
      : {}),
    ...(query.timezone === undefined ? {} : { timezone: query.timezone }),
    include_imported: query.includeImported !== false,
  }
}

function localToday(timeZone: string): string {
  let formatter: Intl.DateTimeFormat
  try {
    formatter = new Intl.DateTimeFormat('en-US', {
      timeZone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    })
  } catch {
    formatter = new Intl.DateTimeFormat('en-US', {
      timeZone: 'UTC',
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    })
  }
  const parts = formatter.formatToParts(new Date())
  const read = (type: Intl.DateTimeFormatPartTypes) =>
    parts.find((part) => part.type === type)?.value ?? ''
  return `${read('year')}-${read('month')}-${read('day')}`
}

function includesToday(query: StatsQuery): boolean {
  const today = localToday(query.timezone ?? 'UTC')
  return query.dateRange.from <= today && today <= query.dateRange.to
}

function missingResponse(status: number): Problem {
  return {
    type: 'about:blank',
    title: 'Empty API response',
    status,
  }
}

export function useStats(
  query: StatsQuery,
): UseQueryResult<QueryResponse, Problem> {
  const body = normalizedBody(query)
  const current = includesToday(query)
  return useQuery<QueryResponse, Problem>({
    queryKey: ['stats', query.siteId, body],
    queryFn: async () => {
      const { data, error, response } = await api.POST('/api/private/query', {
        body,
      })
      if (error) throw error
      if (!data || typeof data === 'string') {
        throw missingResponse(response.status)
      }
      return data
    },
    staleTime: current ? 30_000 : 300_000,
    refetchInterval: query.live === true && current ? 30_000 : false,
  })
}

export function useRealtime(
  siteId: number,
): UseQueryResult<Realtime, Problem> {
  return useQuery<Realtime, Problem>({
    queryKey: ['realtime', siteId],
    queryFn: async () => {
      const { data, error } = await api.GET(
        '/api/private/realtime',
        { params: { query: { site_id: siteId } } },
      )
      if (error) throw error
      return data
    },
    staleTime: 30_000,
    refetchInterval: 30_000,
  })
}
