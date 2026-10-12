import { queryOptions, useQuery, useQueryClient, type QueryKey, type UseQueryOptions, type UseQueryResult } from '@tanstack/react-query'

import { api } from './api'
import type { components } from './api.gen'
import { serializeFilters } from './search'

type DateRange = components['schemas']['DateRange']
type Metric = components['schemas']['Metric']
type Dimension = string
type Filter = components['schemas']['Filter']
type OrderBy = components['schemas']['OrderBy']
type QueryResponse = components['schemas']['QueryResponse']
type Realtime = components['schemas']['RealtimeResponse']
type Problem = components['schemas']['Problem']
type Compare = 'off' | 'previous_period' | 'previous_year'
export type TimeseriesInterval = 'hour' | 'day' | 'week' | 'month'
type TimeseriesApi = {
  GET: (
    path: '/api/private/stats/timeseries',
    options: {
      params: { query: Record<string, string | number | boolean | string[] | undefined> }
      signal?: AbortSignal
    },
  ) => Promise<{ data?: QueryResponse | string; error?: Problem; response: Response }>
}
type BreakdownApi = {
  GET: (
    path: '/api/private/stats/breakdown',
    options: { params: { query: Record<string, string | number | boolean | string[] | undefined> }; signal?: AbortSignal },
  ) => Promise<{ data?: QueryResponse | string; error?: Problem; response: Response }>
}

const timeseriesApi = api as unknown as TimeseriesApi
const breakdownApi = api as unknown as BreakdownApi

export type StatsQuery = {
  siteId: number
  trackingMode?: 'anonymous' | 'identified'
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

function terminalAuthorizationError(error: Problem | null): boolean {
  return error?.status === 401 || error?.status === 403
}

function useStatsRetry(queryKey: QueryKey) {
  const client = useQueryClient()
  const retry = (client.getQueryDefaults(queryKey).retry
    ?? client.getDefaultOptions().queries?.retry
    ?? 3) as NonNullable<UseQueryOptions<unknown, Problem>['retry']>
  return (failureCount: number, error: Problem) => {
    if (terminalAuthorizationError(error)) return false
    if (typeof retry === 'function') return retry(failureCount, error)
    return retry === true || (typeof retry === 'number' && failureCount < retry)
  }
}

export function useStats(
  query: StatsQuery,
): UseQueryResult<QueryResponse, Problem> {
  const body = normalizedBody(query)
  const current = includesToday(query)
  const queryKey = ['stats', query.siteId, body, ...(query.trackingMode ? [query.trackingMode] : [])]
  const retry = useStatsRetry(queryKey)
  return useQuery<QueryResponse, Problem>({
    queryKey,
    retry,
    queryFn: async ({ signal }) => {
      const { data, error, response } = await api.POST('/api/private/query', {
        body,
        signal,
      })
      if (error) throw error
      if (!data || typeof data === 'string') {
        throw missingResponse(response.status)
      }
      return data
    },
    staleTime: current ? 30_000 : 300_000,
    refetchInterval: query.live === true && current
      ? (state) => terminalAuthorizationError(state.state.error) ? false : 30_000
      : false,
  })
}

export function useTimeseries(
  query: StatsQuery,
  interval: TimeseriesInterval,
  enabled = true,
): UseQueryResult<QueryResponse, Problem> {
  const options = timeseriesQueryOptions(query, interval)
  const retry = useStatsRetry(options.queryKey)
  return useQuery<QueryResponse, Problem>({ ...options, enabled, retry })
}

export function timeseriesQueryOptions(query: StatsQuery, interval: TimeseriesInterval) {
  const params = {
    site_id: query.siteId,
    period: 'custom' as const,
    date_from: query.dateRange.from,
    date_to: query.dateRange.to,
    timezone: query.timezone,
    metrics: query.metrics.join(','),
    filter: serializeFilters(query.filters),
    ...(query.compare && query.compare !== 'off' ? { compare: query.compare } : {}),
    interval,
    include_imported: query.includeImported !== false,
  }
  const current = includesToday(query)
  const queryKey = ['stats', query.siteId, 'timeseries', params, ...(query.trackingMode ? [query.trackingMode] : [])]
  return queryOptions<QueryResponse, Problem>({
    queryKey,
    retry: (failureCount, error) => !terminalAuthorizationError(error) && failureCount < 1,
    queryFn: async ({ signal }) => {
      const { data, error, response } = await timeseriesApi.GET(
        '/api/private/stats/timeseries',
        { params: { query: params }, signal },
      )
      if (error) throw error
      if (!data || typeof data === 'string') throw missingResponse(response.status)
      return data
    },
    staleTime: current ? 30_000 : 300_000,
    refetchInterval: query.live === true && current
      ? (state) => terminalAuthorizationError(state.state.error) ? false : 30_000
      : false,
  })
}

export function useBreakdown(query: StatsQuery, enabled = true): UseQueryResult<QueryResponse, Problem> {
  const params = {
    site_id: query.siteId,
    period: 'custom' as const,
    date_from: query.dateRange.from,
    date_to: query.dateRange.to,
    timezone: query.timezone,
    metrics: query.metrics.join(','),
    filter: serializeFilters(query.filters),
    ...(query.compare && query.compare !== 'off' ? { compare: query.compare } : {}),
    dimension: query.dimensions ?? [],
    ...(query.limit === undefined ? {} : { limit: String(query.limit) }),
    include_imported: query.includeImported !== false,
    cursor: query.cursor,
  }
  const current = includesToday(query)
  const queryKey = ['stats', query.siteId, 'breakdown', params, ...(query.trackingMode ? [query.trackingMode] : [])]
  const retry = useStatsRetry(queryKey)
  return useQuery<QueryResponse, Problem>({
    queryKey,
    enabled,
    retry,
    queryFn: async ({ signal }) => {
      const { data, error, response } = await breakdownApi.GET('/api/private/stats/breakdown', { params: { query: params }, signal })
      if (error) throw error
      if (!data || typeof data === 'string') throw missingResponse(response.status)
      return data
    },
    staleTime: current ? 30_000 : 300_000,
  })
}

export function useRealtime(
  siteId: number,
  trackingMode?: StatsQuery['trackingMode'],
): UseQueryResult<Realtime, Problem> {
  const queryKey = ['realtime', siteId, ...(trackingMode ? [trackingMode] : [])]
  const retry = useStatsRetry(queryKey)
  return useQuery<Realtime, Problem>({
    queryKey,
    retry,
    queryFn: async () => {
      const { data, error } = await api.GET(
        '/api/private/realtime',
        { params: { query: { site_id: siteId } } },
      )
      if (error) throw error
      return data
    },
    staleTime: 30_000,
    refetchInterval: (state) => terminalAuthorizationError(state.state.error) ? false : 30_000,
  })
}
