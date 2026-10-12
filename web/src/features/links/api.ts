import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api'

import type { LinkInput, LinkStats, PixelSnippet, PixelStats, ShortLink } from './types'

type Problem = { detail?: string }
type ApiResult<T> = Promise<{ data?: T; error?: Problem; response: Response }>
type Range = { from: string; to: string }
type LinksApi = {
  GET: {
    (path: '/api/private/sites/{site}/links', options: { params: { path: { site: number } } }): ApiResult<ShortLink[]>
    (path: '/api/private/links/{link}', options: { params: { path: { link: number } } }): ApiResult<ShortLink>
    (
      path: '/api/private/links/{link}/stats',
      options: { params: { path: { link: number }; query: Range } },
    ): ApiResult<LinkStats>
    (
      path: '/api/private/sites/{site}/pixel',
      options: { params: { path: { site: number }; query: { campaign?: string } } },
    ): ApiResult<PixelSnippet>
    (
      path: '/api/private/sites/{site}/pixel/stats',
      options: { params: { path: { site: number }; query: Range } },
    ): ApiResult<PixelStats>
  }
  POST: (
    path: '/api/private/sites/{site}/links',
    options: { params: { path: { site: number } }; body: LinkInput },
  ) => ApiResult<ShortLink>
  PATCH: (
    path: '/api/private/links/{link}',
    options: { params: { path: { link: number } }; body: Partial<LinkInput> },
  ) => ApiResult<ShortLink>
  DELETE: (
    path: '/api/private/links/{link}',
    options: { params: { path: { link: number } } },
  ) => ApiResult<never>
}

const linksApi = api as unknown as LinksApi

export const linkKeys = {
  all: (siteId: number) => ['links', siteId] as const,
  one: (siteId: number, id: number) => ['links', siteId, 'one', id] as const,
  stats: (id: number, range: Range) => ['links', 'stats', id, range] as const,
  pixel: (siteId: number, campaign: string) => ['links', siteId, 'pixel', campaign] as const,
  pixelStats: (siteId: number, range: Range) => ['links', siteId, 'pixel-stats', range] as const,
}

function requireData<T>(result: { data?: T; error?: Problem }, message: string): T {
  if (result.error) throw new Error(result.error.detail ?? message)
  if (result.data === undefined) throw new Error(message)
  return result.data
}

export function useLinks(siteId: number) {
  return useQuery<ShortLink[], Error>({
    queryKey: linkKeys.all(siteId),
    queryFn: async () =>
      requireData(
        await linksApi.GET('/api/private/sites/{site}/links', { params: { path: { site: siteId } } }),
        'Could not load links.',
      ),
  })
}

export function useLink(siteId: number, id: number) {
  return useQuery<ShortLink, Error>({
    queryKey: linkKeys.one(siteId, id),
    queryFn: async () =>
      requireData(
        await linksApi.GET('/api/private/links/{link}', { params: { path: { link: id } } }),
        'Could not load the link.',
      ),
  })
}

export function useLinkStats(id: number, range: Range) {
  return useQuery<LinkStats, Error>({
    queryKey: linkKeys.stats(id, range),
    queryFn: async () =>
      requireData(
        await linksApi.GET('/api/private/links/{link}/stats', { params: { path: { link: id }, query: range } }),
        'Could not load click statistics.',
      ),
  })
}

export function usePixelSnippet(siteId: number, campaign: string, enabled = true) {
  return useQuery<PixelSnippet, Error>({
    queryKey: linkKeys.pixel(siteId, campaign),
    enabled,
    queryFn: async () =>
      requireData(
        await linksApi.GET('/api/private/sites/{site}/pixel', {
          params: { path: { site: siteId }, query: campaign ? { campaign } : {} },
        }),
        'Could not build the pixel snippet.',
      ),
  })
}

export function usePixelStats(siteId: number, range: Range) {
  return useQuery<PixelStats, Error>({
    queryKey: linkKeys.pixelStats(siteId, range),
    queryFn: async () =>
      requireData(
        await linksApi.GET('/api/private/sites/{site}/pixel/stats', {
          params: { path: { site: siteId }, query: range },
        }),
        'Could not load impression statistics.',
      ),
  })
}

export function useSaveLink(siteId: number) {
  const client = useQueryClient()
  return useMutation<ShortLink, Error, { id?: number; input: Partial<LinkInput> }>({
    mutationFn: async ({ id, input }) =>
      id === undefined
        ? requireData(
            await linksApi.POST('/api/private/sites/{site}/links', {
              params: { path: { site: siteId } },
              body: input as LinkInput,
            }),
            'Could not create the link.',
          )
        : requireData(
            await linksApi.PATCH('/api/private/links/{link}', { params: { path: { link: id } }, body: input }),
            'Could not save the link.',
          ),
    onSuccess: () => client.invalidateQueries({ queryKey: linkKeys.all(siteId) }),
  })
}

export function useDeleteLink(siteId: number) {
  const client = useQueryClient()
  return useMutation<void, Error, number>({
    mutationFn: async (id) => {
      const result = await linksApi.DELETE('/api/private/links/{link}', { params: { path: { link: id } } })
      if (result.error) throw new Error(result.error.detail ?? 'Could not delete the link.')
    },
    onSuccess: () => client.invalidateQueries({ queryKey: linkKeys.all(siteId) }),
  })
}
