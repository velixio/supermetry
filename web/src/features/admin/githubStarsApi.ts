import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import type { components } from '@/lib/api.gen'
import { ApiProblem } from '@/lib/apiProblem'
import { privateRequest } from '@/features/settings/privateApi'

type Schemas = components['schemas']
export type StarsSummary = Schemas['GitHubStarsSummary']
export type StarsPoint = Schemas['GitHubStarsPoint']
export type StarsSeries = Schemas['GitHubStarsSeries']
export type StarsEvent = Schemas['GitHubStarsEvent']
export type StarsAnnotation = Schemas['GitHubStarsAnnotation']
export type StarsGranularity = 'hour' | 'day'

const base = '/api/private/admin/github-stars'
export const summaryRefetchMs = 10_000
export const eventsPageSize = 50

export const starsKeys = {
  all: ['admin', 'github-stars'] as const,
  summary: ['admin', 'github-stars', 'summary'] as const,
  series: (granularity: StarsGranularity) => ['admin', 'github-stars', 'series', granularity] as const,
  events: ['admin', 'github-stars', 'events'] as const,
  annotations: ['admin', 'github-stars', 'annotations'] as const,
}

/** The API answers 404 for every star route while SUPERMETRY_GITHUB_STARS_REPO is empty. */
export function isNotConfigured(error: unknown): boolean {
  return error instanceof ApiProblem && error.status === 404
}

const noRetryWhenOff = (count: number, error: unknown) => !isNotConfigured(error) && count < 2

/** Live counters; polled every 10 seconds. */
export function useStarsSummary() {
  return useQuery({
    queryKey: starsKeys.summary,
    queryFn: () => privateRequest<StarsSummary>('GET', `${base}/summary`),
    refetchInterval: (query) => (isNotConfigured(query.state.error) ? false : summaryRefetchMs),
    retry: noRetryWhenOff,
  })
}

export function useStarsSeries(granularity: StarsGranularity, enabled = true) {
  return useQuery({
    queryKey: starsKeys.series(granularity),
    queryFn: () => privateRequest<StarsSeries>('GET', `${base}/series?granularity=${granularity}`),
    enabled,
    retry: noRetryWhenOff,
    placeholderData: keepPreviousData,
    refetchInterval: summaryRefetchMs * 6,
  })
}

export function useStarsEvents(enabled = true) {
  return useInfiniteQuery({
    queryKey: starsKeys.events,
    queryFn: ({ pageParam }) => privateRequest<{ data: StarsEvent[]; next_cursor: string | null }>(
      'GET', `${base}/events?limit=${eventsPageSize}${pageParam ? `&cursor=${encodeURIComponent(pageParam)}` : ''}`),
    initialPageParam: '',
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled,
    retry: noRetryWhenOff,
  })
}

export function useStarsAnnotations(enabled = true) {
  return useQuery({
    queryKey: starsKeys.annotations,
    queryFn: () => privateRequest<{ data: StarsAnnotation[] }>('GET', `${base}/annotations`),
    enabled,
    retry: noRetryWhenOff,
  })
}

export function useAddStarsAnnotation() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { at: string; label: string }) => privateRequest<StarsAnnotation>('POST', `${base}/annotations`, { body }),
    onSuccess: () => client.invalidateQueries({ queryKey: starsKeys.annotations }),
  })
}

export function useDeleteStarsAnnotation() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => privateRequest<void>('DELETE', `${base}/annotations/${id}`),
    onSuccess: () => client.invalidateQueries({ queryKey: starsKeys.annotations }),
  })
}

/** Privacy erasure: deletes every recorded event of one GitHub login. */
export function useEraseStarsUser() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (login: string) => privateRequest<{ deleted_events: number }>('DELETE', `${base}/users/${encodeURIComponent(login)}`),
    onSuccess: () => client.invalidateQueries({ queryKey: starsKeys.all }),
  })
}
