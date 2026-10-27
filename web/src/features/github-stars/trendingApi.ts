import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import type { components } from '@/lib/api.gen'
import { privateRequest } from '@/features/settings/privateApi'
import { starsKeys } from './githubStarsApi'
import { useStarsOrg } from './starsOrg'

type Schemas = components['schemas']
export type TrendingStatus = Schemas['GitHubTrendingStatus']
export type TrendingSighting = Schemas['GitHubTrendingSighting']
export type TrendingList = Schemas['GitHubTrendingList']

const baseOf = (orgId: number) => `/api/private/orgs/${orgId}/github-stars`
export const trendingRefetchMs = 60_000

export const trendingKeys = {
  repo: (orgId: number, repoId: number) => [...starsKeys.repo(orgId, repoId), 'trending'] as const,
}

export function useTrending(repoId: number) {
  const { orgId } = useStarsOrg()
  return useQuery({
    queryKey: trendingKeys.repo(orgId, repoId),
    queryFn: () => privateRequest<TrendingStatus>('GET', `${baseOf(orgId)}/repos/${repoId}/trending`, { orgId }),
    refetchInterval: trendingRefetchMs,
  })
}

/** Opts the repository in or out of the trending watcher (owners and admins). */
export function useSetTrendingEnabled(repoId: number) {
  const { orgId } = useStarsOrg()
  const client = useQueryClient()
  return useMutation({
    mutationFn: (trending_enabled: boolean) => privateRequest<unknown>('PATCH', `${baseOf(orgId)}/repos/${repoId}`, { orgId, body: { trending_enabled } }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: trendingKeys.repo(orgId, repoId) })
      // Entering the list adds a chart marker.
      void client.invalidateQueries({ queryKey: starsKeys.annotations(orgId, repoId) })
    },
  })
}
