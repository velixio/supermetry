import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import type { components } from '@/lib/api.gen'
import { privateRequest } from '@/features/settings/privateApi'

type Schemas = components['schemas']
export type TrendingStatus = Schemas['GitHubTrendingStatus']
export type TrendingSighting = Schemas['GitHubTrendingSighting']
export type TrendingList = Schemas['GitHubTrendingList']

// Rebase note: with per-org star tracking this becomes /api/private/orgs/{org}/github-stars.
const base = '/api/private/admin/github-stars'
export const trendingRefetchMs = 60_000

export const trendingKeys = {
  repo: (repoId: number) => ['admin', 'github-stars', 'repo', repoId, 'trending'] as const,
}

export function useTrending(repoId: number) {
  return useQuery({
    queryKey: trendingKeys.repo(repoId),
    queryFn: () => privateRequest<TrendingStatus>('GET', `${base}/repos/${repoId}/trending`),
    refetchInterval: trendingRefetchMs,
  })
}

/** Opts the repository in or out of the trending watcher. */
export function useSetTrendingEnabled(repoId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (trending_enabled: boolean) => privateRequest<unknown>('PATCH', `${base}/repos/${repoId}`, { body: { trending_enabled } }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: trendingKeys.repo(repoId) })
      // Entering the list adds a chart marker.
      void client.invalidateQueries({ queryKey: ['admin', 'github-stars', 'repo', repoId, 'annotations'] })
    },
  })
}
