import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { privateRequest } from '@/features/settings/privateApi'
import type { components } from '@/lib/api.gen'

import { appQuery, type AppSearch } from '../search'

export type AppActiveUsersData = components['schemas']['AppActiveUsers']
export type AppAdoptionData = components['schemas']['AppAdoption']
export type AppAdoptionRow = components['schemas']['AppAdoptionRow']
export type AppEngagementData = components['schemas']['AppEngagement']
export type AppFeatureSetData = components['schemas']['AppFeatureSet']
export type AppFeatureAdoptionData = components['schemas']['AppFeatureAdoption']
export type AdoptionDimension = 'release' | 'platform' | 'country' | 'os' | 'device'

const base = (siteId: number) => `/api/private/sites/${siteId}/app/product`

export function useActiveUsers(siteId: number, search: AppSearch, timezone: string) {
  const query = appQuery(search, timezone).toString()
  return useQuery({ queryKey: ['app-product', 'active-users', siteId, query], queryFn: () => privateRequest<AppActiveUsersData>('GET', `${base(siteId)}/active-users?${query}`) })
}

export function useAdoption(siteId: number, search: AppSearch, timezone: string, by: AdoptionDimension) {
  const query = appQuery(search, timezone, { by }).toString()
  return useQuery({ queryKey: ['app-product', 'adoption', siteId, query], queryFn: () => privateRequest<AppAdoptionData>('GET', `${base(siteId)}/adoption?${query}`) })
}

export function useEngagement(siteId: number, search: AppSearch, timezone: string) {
  const query = appQuery(search, timezone).toString()
  return useQuery({ queryKey: ['app-product', 'engagement', siteId, query], queryFn: () => privateRequest<AppEngagementData>('GET', `${base(siteId)}/engagement?${query}`) })
}

export function useFeatureSets(siteId: number) {
  return useQuery({ queryKey: ['app-product', 'features', siteId], queryFn: () => privateRequest<{ features: AppFeatureSetData[] }>('GET', `${base(siteId)}/features`) })
}

export function useFeatureAdoption(siteId: number, id: number, search: AppSearch, timezone: string) {
  const query = appQuery(search, timezone).toString()
  return useQuery({ queryKey: ['app-product', 'feature-adoption', siteId, id, query], queryFn: () => privateRequest<AppFeatureAdoptionData>('GET', `${base(siteId)}/features/${id}/adoption?${query}`) })
}

export function useCreateFeatureSet(siteId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: { name: string; event_names: string[] }) => privateRequest<AppFeatureSetData>('POST', `${base(siteId)}/features`, { body: input }),
    onSuccess: () => client.invalidateQueries({ queryKey: ['app-product', 'features', siteId] }),
  })
}

export function useDeleteFeatureSet(siteId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => privateRequest<void>('DELETE', `${base(siteId)}/features/${id}`),
    onSuccess: () => client.invalidateQueries({ queryKey: ['app-product', 'features', siteId] }),
  })
}

/** Stickiness as a whole percent, or n/a. */
export function formatStickiness(value: number | null | undefined): string {
  return value === null || value === undefined ? 'n/a' : `${Math.round(value * 100)}%`
}

export function formatShare(value: number | null | undefined): string {
  return value === null || value === undefined ? 'n/a' : `${(value * 100).toFixed(1)}%`
}

export function formatSeconds(value: number | null | undefined): string {
  if (value === null || value === undefined) return 'n/a'
  const rounded = Math.round(value)
  return rounded >= 60 ? `${Math.floor(rounded / 60)}m ${rounded % 60}s` : `${rounded}s`
}

/** Parses a feature-set form: one event name per line or comma, trimmed, de-duplicated; returns an error message or null. */
export function parseFeatureNames(text: string): { names: string[]; error: string | null } {
  const names = [...new Set(text.split(/[\n,]/).map((item) => item.trim()).filter(Boolean))]
  if (names.length === 0) return { names, error: 'Enter at least one event name.' }
  if (names.length > 20) return { names, error: 'A feature set holds at most 20 event names.' }
  return { names, error: null }
}
