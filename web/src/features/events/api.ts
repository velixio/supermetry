import { useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api'
import type { components } from '@/lib/api.gen'
import { serializeFilters, type Filter } from '@/lib/search'

import type { EventExploreList, PropertyKeyCount, PropertyValueCount, Suggestion } from './types'

type Problem = components['schemas']['Problem']
type ApiResult<T> = Promise<{ data?: T; error?: Problem; response: Response }>
type ExploreApi = {
  GET: <T>(path: string, options: Record<string, unknown>) => ApiResult<T>
}

const exploreApi = api as unknown as ExploreApi

type DateRange = { from: string; to: string }

export const eventKeys = {
  site: (siteId: number) => ['sites', siteId, 'events'] as const,
  list: (siteId: number, range: DateRange, filters: Filter[], name: string, cursor?: string) =>
    [...eventKeys.site(siteId), 'list', { range, filters, name, cursor }] as const,
  properties: (siteId: number, range: DateRange, event: string) =>
    [...eventKeys.site(siteId), 'properties', { range, event }] as const,
  propertyValues: (siteId: number, key: string, range: DateRange, event: string) =>
    [...eventKeys.site(siteId), 'properties', key, 'values', { range, event }] as const,
  suggestions: (siteId: number, dimension: string, query: string, range: DateRange) =>
    [...eventKeys.site(siteId), 'suggestions', { dimension, query, range }] as const,
}

async function request<T>(path: string, orgId: number, query: Record<string, unknown>): Promise<T> {
  const { data, error, response } = await exploreApi.GET<T>(path, {
    params: { header: { 'X-Supermetry-Org': String(orgId) }, query },
  })
  if (error || !response.ok) throw new Error(error?.detail ?? `Request failed (${response.status})`)
  if (data === undefined) throw new Error('The response was empty.')
  return data
}

export function useEvents(siteId: number, orgId: number, range: DateRange, filters: Filter[], name: string, cursor?: string) {
  return useQuery<EventExploreList, Error>({
    queryKey: eventKeys.list(siteId, range, filters, name, cursor),
    queryFn: () => request(`/api/private/sites/${siteId}/events`, orgId, {
      from: range.from, to: range.to, name: name || undefined,
      filter: serializeFilters(filters), cursor, limit: 50,
    }),
  })
}

export function useProperties(siteId: number, orgId: number, range: DateRange, event: string) {
  return useQuery<PropertyKeyCount[], Error>({
    queryKey: eventKeys.properties(siteId, range, event),
    queryFn: () => request(`/api/private/sites/${siteId}/properties`, orgId, {
      from: range.from, to: range.to, event: event || undefined,
    }),
  })
}

export function usePropertyValues(siteId: number, orgId: number, key: string, range: DateRange, event: string) {
  return useQuery<PropertyValueCount[], Error>({
    queryKey: eventKeys.propertyValues(siteId, key, range, event),
    queryFn: () => request(`/api/private/sites/${siteId}/properties/${encodeURIComponent(key)}/values`, orgId, {
      from: range.from, to: range.to, event: event || undefined,
    }),
    enabled: key !== '',
  })
}

export function useSuggestions(siteId: number, orgId: number, range: DateRange, dimension: string, query: string, enabled = true) {
  return useQuery<Suggestion[], Error>({
    queryKey: eventKeys.suggestions(siteId, dimension, query, range),
    queryFn: () => request(`/api/private/sites/${siteId}/suggestions`, orgId, {
      dimension, q: query || undefined, from: range.from, to: range.to,
    }),
    enabled: enabled && dimension !== '' && range.from !== '' && range.to !== '',
    staleTime: 30_000,
  })
}
