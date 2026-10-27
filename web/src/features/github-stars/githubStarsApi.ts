import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import type { components } from '@/lib/api.gen'
import { privateRequest } from '@/features/settings/privateApi'
import { useStarsOrg } from './starsOrg'

type Schemas = components['schemas']
export type StarsRepo = Schemas['GitHubStarsRepo']
export type StarsRepoSecret = Schemas['GitHubStarsRepoSecret']
export type StarsSummary = Schemas['GitHubStarsSummary']
export type StarsPoint = Schemas['GitHubStarsPoint']
export type StarsSeries = Schemas['GitHubStarsSeries']
export type StarsEvent = Schemas['GitHubStarsEvent']
export type StarsAnnotation = Schemas['GitHubStarsAnnotation']
export type StarsGranularity = 'hour' | 'day' | 'month'
export type StarsRepoPatch = Schemas['GitHubStarsRepoPatch']

const baseOf = (orgId: number) => `/api/private/orgs/${orgId}/github-stars`
export const summaryRefetchMs = 10_000
export const eventsPageSize = 50

export const starsKeys = {
  all: (orgId: number) => ['org', orgId, 'github-stars'] as const,
  repos: (orgId: number) => ['org', orgId, 'github-stars', 'repos'] as const,
  repo: (orgId: number, repoId: number) => ['org', orgId, 'github-stars', 'repo', repoId] as const,
  summary: (orgId: number, repoId: number) => ['org', orgId, 'github-stars', 'repo', repoId, 'summary'] as const,
  series: (orgId: number, repoId: number, granularity: StarsGranularity) => ['org', orgId, 'github-stars', 'repo', repoId, 'series', granularity] as const,
  events: (orgId: number, repoId: number) => ['org', orgId, 'github-stars', 'repo', repoId, 'events'] as const,
  annotations: (orgId: number, repoId: number) => ['org', orgId, 'github-stars', 'repo', repoId, 'annotations'] as const,
}

const query = (repoId: number) => `repo_id=${repoId}`

/** The tracked repositories. While any repository has not synced yet the list refreshes so its status fills in. */
export function useStarsRepos() {
  const { orgId } = useStarsOrg()
  return useQuery({
    queryKey: starsKeys.repos(orgId),
    queryFn: () => privateRequest<{ data: StarsRepo[] }>('GET', `${baseOf(orgId)}/repos`, { orgId }),
    refetchInterval: (q) => (q.state.data?.data.some((repo) => repo.enabled && !repo.last_reconcile_at) ? 5_000 : false),
  })
}

export function useAddStarsRepo() {
  const { orgId } = useStarsOrg()
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { repo: string; token?: string; time_zone?: string; public_counter?: boolean }) => privateRequest<StarsRepoSecret>('POST', `${baseOf(orgId)}/repos`, { orgId, body }),
    onSuccess: () => client.invalidateQueries({ queryKey: starsKeys.repos(orgId) }),
  })
}

export function useUpdateStarsRepo() {
  const { orgId } = useStarsOrg()
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, patch }: { id: number; patch: StarsRepoPatch }) => privateRequest<StarsRepo>('PATCH', `${baseOf(orgId)}/repos/${id}`, { orgId, body: patch }),
    onSuccess: () => client.invalidateQueries({ queryKey: starsKeys.repos(orgId) }),
  })
}

/** deleteData=false only disables the repository and keeps its history; true removes it with all its data. */
export function useDeleteStarsRepo() {
  const { orgId } = useStarsOrg()
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, deleteData }: { id: number; deleteData: boolean }) => privateRequest<StarsRepo | undefined>('DELETE', `${baseOf(orgId)}/repos/${id}?delete_data=${deleteData}`, { orgId }),
    onSuccess: () => client.invalidateQueries({ queryKey: starsKeys.all(orgId) }),
  })
}

export function useRotateStarsSecret() {
  const { orgId } = useStarsOrg()
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => privateRequest<StarsRepoSecret>('POST', `${baseOf(orgId)}/repos/${id}/rotate-secret`, { orgId }),
    onSuccess: () => client.invalidateQueries({ queryKey: starsKeys.repos(orgId) }),
  })
}

export function useSyncStarsRepo() {
  const { orgId } = useStarsOrg()
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => privateRequest<void>('POST', `${baseOf(orgId)}/repos/${id}/sync`, { orgId }),
    // The sync runs in the background; look again shortly for the outcome.
    onSuccess: () => { window.setTimeout(() => void client.invalidateQueries({ queryKey: starsKeys.all(orgId) }), 3_000) },
  })
}

/** Live counters of one repository; polled every 10 seconds. */
export function useStarsSummary(repoId: number) {
  const { orgId } = useStarsOrg()
  return useQuery({
    queryKey: starsKeys.summary(orgId, repoId),
    queryFn: () => privateRequest<StarsSummary>('GET', `${baseOf(orgId)}/summary?${query(repoId)}`, { orgId }),
    refetchInterval: summaryRefetchMs,
  })
}

export function useStarsSeries(repoId: number, granularity: StarsGranularity) {
  const { orgId } = useStarsOrg()
  return useQuery({
    queryKey: starsKeys.series(orgId, repoId, granularity),
    queryFn: () => privateRequest<StarsSeries>('GET', `${baseOf(orgId)}/series?${query(repoId)}&granularity=${granularity}`, { orgId }),
    placeholderData: keepPreviousData,
    refetchInterval: summaryRefetchMs * 6,
  })
}

export function useStarsEvents(repoId: number) {
  const { orgId } = useStarsOrg()
  return useInfiniteQuery({
    queryKey: starsKeys.events(orgId, repoId),
    queryFn: ({ pageParam }) => privateRequest<{ data: StarsEvent[]; next_cursor: string | null }>(
      'GET', `${baseOf(orgId)}/events?${query(repoId)}&limit=${eventsPageSize}${pageParam ? `&cursor=${encodeURIComponent(pageParam)}` : ''}`, { orgId }),
    initialPageParam: '',
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  })
}

export function useStarsAnnotations(repoId: number) {
  const { orgId } = useStarsOrg()
  return useQuery({
    queryKey: starsKeys.annotations(orgId, repoId),
    queryFn: () => privateRequest<{ data: StarsAnnotation[] }>('GET', `${baseOf(orgId)}/annotations?${query(repoId)}`, { orgId }),
  })
}

export function useAddStarsAnnotation(repoId: number) {
  const { orgId } = useStarsOrg()
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { at: string; label: string }) => privateRequest<StarsAnnotation>('POST', `${baseOf(orgId)}/annotations?${query(repoId)}`, { orgId, body }),
    onSuccess: () => client.invalidateQueries({ queryKey: starsKeys.annotations(orgId, repoId) }),
  })
}

export function useDeleteStarsAnnotation(repoId: number) {
  const { orgId } = useStarsOrg()
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => privateRequest<void>('DELETE', `${baseOf(orgId)}/annotations/${id}?${query(repoId)}`, { orgId }),
    onSuccess: () => client.invalidateQueries({ queryKey: starsKeys.annotations(orgId, repoId) }),
  })
}

/** Privacy erasure: deletes every recorded event of one GitHub login and ignores their future stars. */
export function useEraseStarsUser(repoId: number) {
  const { orgId } = useStarsOrg()
  const client = useQueryClient()
  return useMutation({
    mutationFn: (login: string) => privateRequest<{ deleted_events: number }>('DELETE', `${baseOf(orgId)}/users/${encodeURIComponent(login)}?${query(repoId)}`, { orgId }),
    onSuccess: () => client.invalidateQueries({ queryKey: starsKeys.repo(orgId, repoId) }),
  })
}
