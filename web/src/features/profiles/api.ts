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
): Promise<T> {
  const client = api as unknown as LooseClient
  const result = await client[method](path, {
    params: { header: { 'X-Panel-Org': String(orgId) }, query },
  })
  if (!result.response.ok) {
    const problem = result.error as { detail?: string } | undefined
    throw new Error(problem?.detail ?? `Request failed (${result.response.status})`)
  }
  return result.data as T
}

export const profileKeys = {
  all: (siteId: number) => ['profiles', siteId] as const,
  list: (siteId: number, search: string, cursor?: string) => ['profiles', siteId, 'list', { search, cursor }] as const,
  detailRoot: (profileId: number) => ['profiles', 'detail', profileId] as const,
  detail: (profileId: number, cursor?: string) => [...profileKeys.detailRoot(profileId), { cursor }] as const,
}

export function useProfiles(siteId: number, orgId: number, search: string, cursor?: string) {
  return useQuery<ProfilesResponse, Error>({
    queryKey: profileKeys.list(siteId, search, cursor),
    queryFn: () => profilesRequest('GET', `/api/private/sites/${siteId}/profiles`, orgId, {
      search: search || undefined,
      cursor,
      limit: 25,
    }),
  })
}

export function useProfile(profileId: number, orgId: number, cursor?: string) {
  return useQuery<ProfileResponse, Error>({
    queryKey: profileKeys.detail(profileId, cursor),
    queryFn: () => profilesRequest('GET', `/api/private/profiles/${profileId}`, orgId, { cursor, limit: 50 }),
  })
}

export function useDeleteProfile(siteId: number, orgId: number, onDeleted: () => void) {
  const queryClient = useQueryClient()
  return useMutation<void, Error, number>({
    mutationFn: (profileId) => profilesRequest('DELETE', `/api/private/profiles/${profileId}`, orgId),
    onSuccess: async (_data, profileId) => {
      queryClient.removeQueries({ queryKey: profileKeys.detailRoot(profileId) })
      await queryClient.invalidateQueries({ queryKey: profileKeys.all(siteId) })
      onDeleted()
    },
  })
}
