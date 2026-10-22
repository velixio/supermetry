import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import type { components } from '@/lib/api.gen'
import { privateRequest } from '@/features/settings/privateApi'

type Schemas = components['schemas']
export type StarsRepo = Schemas['GitHubStarsRepo']
export type StarsRepoSecret = Schemas['GitHubStarsRepoSecret']
export type StarsSummary = Schemas['GitHubStarsSummary']
export type StarsPoint = Schemas['GitHubStarsPoint']
export type StarsSeries = Schemas['GitHubStarsSeries']
export type StarsEvent = Schemas['GitHubStarsEvent']
export type StarsAnnotation = Schemas['GitHubStarsAnnotation']
export type StarsGranularity = 'hour' | 'day'
export type StarsRepoPatch = Schemas['GitHubStarsRepoPatch']

const base = '/api/private/admin/github-stars'
export const summaryRefetchMs = 10_000
export const eventsPageSize = 50

export const starsKeys = {
  all: ['admin', 'github-stars'] as const,
  repos: ['admin', 'github-stars', 'repos'] as const,
  repo: (repoId: number) => ['admin', 'github-stars', 'repo', repoId] as const,
  summary: (repoId: number) => ['admin', 'github-stars', 'repo', repoId, 'summary'] as const,
  series: (repoId: number, granularity: StarsGranularity) => ['admin', 'github-stars', 'repo', repoId, 'series', granularity] as const,
  events: (repoId: number) => ['admin', 'github-stars', 'repo', repoId, 'events'] as const,
  annotations: (repoId: number) => ['admin', 'github-stars', 'repo', repoId, 'annotations'] as const,
}

const query = (repoId: number) => `repo_id=${repoId}`

/** The tracked repositories. While any repository has not synced yet the list refreshes so its status fills in. */
export function useStarsRepos() {
  return useQuery({
    queryKey: starsKeys.repos,
    queryFn: () => privateRequest<{ data: StarsRepo[] }>('GET', `${base}/repos`),
    refetchInterval: (q) => (q.state.data?.data.some((repo) => repo.enabled && !repo.last_reconcile_at) ? 5_000 : false),
  })
}

export function useAddStarsRepo() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { repo: string; token?: string; time_zone?: string }) => privateRequest<StarsRepoSecret>('POST', `${base}/repos`, { body }),
    onSuccess: () => client.invalidateQueries({ queryKey: starsKeys.repos }),
  })
}

export function useUpdateStarsRepo() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, patch }: { id: number; patch: StarsRepoPatch }) => privateRequest<StarsRepo>('PATCH', `${base}/repos/${id}`, { body: patch }),
    onSuccess: () => client.invalidateQueries({ queryKey: starsKeys.repos }),
  })
}

/** deleteData=false only disables the repository and keeps its history; true removes it with all its data. */
export function useDeleteStarsRepo() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, deleteData }: { id: number; deleteData: boolean }) => privateRequest<StarsRepo | undefined>('DELETE', `${base}/repos/${id}?delete_data=${deleteData}`),
    onSuccess: () => client.invalidateQueries({ queryKey: starsKeys.all }),
  })
}

export function useRotateStarsSecret() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => privateRequest<StarsRepoSecret>('POST', `${base}/repos/${id}/rotate-secret`),
    onSuccess: () => client.invalidateQueries({ queryKey: starsKeys.repos }),
  })
}

export function useSyncStarsRepo() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => privateRequest<void>('POST', `${base}/repos/${id}/sync`),
    // The sync runs in the background; look again shortly for the outcome.
    onSuccess: () => { window.setTimeout(() => void client.invalidateQueries({ queryKey: starsKeys.all }), 3_000) },
  })
}

/** Live counters of one repository; polled every 10 seconds. */
export function useStarsSummary(repoId: number) {
  return useQuery({
    queryKey: starsKeys.summary(repoId),
    queryFn: () => privateRequest<StarsSummary>('GET', `${base}/summary?${query(repoId)}`),
    refetchInterval: summaryRefetchMs,
  })
}

export function useStarsSeries(repoId: number, granularity: StarsGranularity) {
  return useQuery({
    queryKey: starsKeys.series(repoId, granularity),
    queryFn: () => privateRequest<StarsSeries>('GET', `${base}/series?${query(repoId)}&granularity=${granularity}`),
    placeholderData: keepPreviousData,
    refetchInterval: summaryRefetchMs * 6,
  })
}

export function useStarsEvents(repoId: number) {
  return useInfiniteQuery({
    queryKey: starsKeys.events(repoId),
    queryFn: ({ pageParam }) => privateRequest<{ data: StarsEvent[]; next_cursor: string | null }>(
      'GET', `${base}/events?${query(repoId)}&limit=${eventsPageSize}${pageParam ? `&cursor=${encodeURIComponent(pageParam)}` : ''}`),
    initialPageParam: '',
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  })
}

export function useStarsAnnotations(repoId: number) {
  return useQuery({
    queryKey: starsKeys.annotations(repoId),
    queryFn: () => privateRequest<{ data: StarsAnnotation[] }>('GET', `${base}/annotations?${query(repoId)}`),
  })
}

export function useAddStarsAnnotation(repoId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { at: string; label: string }) => privateRequest<StarsAnnotation>('POST', `${base}/annotations?${query(repoId)}`, { body }),
    onSuccess: () => client.invalidateQueries({ queryKey: starsKeys.annotations(repoId) }),
  })
}

export function useDeleteStarsAnnotation(repoId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => privateRequest<void>('DELETE', `${base}/annotations/${id}?${query(repoId)}`),
    onSuccess: () => client.invalidateQueries({ queryKey: starsKeys.annotations(repoId) }),
  })
}

/** Privacy erasure: deletes every recorded event of one GitHub login and ignores their future stars. */
export function useEraseStarsUser(repoId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (login: string) => privateRequest<{ deleted_events: number }>('DELETE', `${base}/users/${encodeURIComponent(login)}?${query(repoId)}`),
    onSuccess: () => client.invalidateQueries({ queryKey: starsKeys.repo(repoId) }),
  })
}
