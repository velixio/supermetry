import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api'

import type { ProfileResponse, ProfilesResponse } from './types'

type Method = 'GET' | 'DELETE'
type ApiResult = { data?: unknown; error?: unknown; response: Response }
type LooseClient = Record<Method, (path: string, options: Record<string, unknown>) => Promise<ApiResult>>

async function profilesRequest<T>(
  method: Method,
  path: string,
  orgId: number,
  query?: Record<string, string | number | undefined>,
  signal?: AbortSignal,
): Promise<T> {
  const client = api as unknown as LooseClient
  const result = await client[method](path, {
    params: { header: { 'X-Supermetry-Org': String(orgId) }, query },
    signal,
  })
  if (!result.response.ok) {
    const problem = result.error as { detail?: string } | undefined
    throw new Error(problem?.detail ?? `Request failed (${result.response.status})`)
  }
  return result.data as T
}

export const profileKeys = {
  all: (orgId: number, siteId: number) => ['profiles', orgId, siteId] as const,
  list: (orgId: number, siteId: number, search: string, cursor?: string) => [...profileKeys.all(orgId, siteId), 'list', { search, cursor }] as const,
  detailRoot: (orgId: number, siteId: number, profileId: number) => [...profileKeys.all(orgId, siteId), 'detail', profileId] as const,
  detail: (orgId: number, siteId: number, profileId: number, cursor?: string) => [...profileKeys.detailRoot(orgId, siteId, profileId), { cursor }] as const,
}

export function useProfiles(siteId: number, orgId: number, search: string, cursor?: string) {
  return useQuery<ProfilesResponse, Error>({
    queryKey: profileKeys.list(orgId, siteId, search, cursor),
    queryFn: ({ signal }) => profilesRequest('GET', `/api/private/sites/${siteId}/profiles`, orgId, {
      search: search || undefined,
      cursor,
      limit: 25,
    }, signal),
  })
}

export function useProfile(profileId: number, orgId: number, siteId: number, cursor?: string) {
  return useQuery<ProfileResponse, Error>({
    queryKey: profileKeys.detail(orgId, siteId, profileId, cursor),
    enabled: siteId > 0 && orgId > 0,
    queryFn: async ({ signal }) => {
      const profile = await profilesRequest<ProfileResponse>('GET', `/api/private/profiles/${profileId}`, orgId, { cursor, limit: 50 }, signal)
      if (profile.site_id !== siteId) throw new Error('This profile does not belong to the selected site.')
      return profile
    },
  })
}

export function useDeleteProfile(siteId: number, orgId: number, onDeleted: () => void) {
  const queryClient = useQueryClient()
  return useMutation<void, Error, number>({
    mutationFn: (profileId) => profilesRequest('DELETE', `/api/private/profiles/${profileId}`, orgId),
    onSuccess: async (_data, profileId) => {
      queryClient.removeQueries({ queryKey: profileKeys.detailRoot(orgId, siteId, profileId) })
      await queryClient.invalidateQueries({ queryKey: profileKeys.all(orgId, siteId) })
      onDeleted()
    },
  })
}
