import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api'
import { apiProblem } from '@/lib/apiProblem'

import type { LinkInput, LinkStats, PixelSnippet, PixelStats, ShortLink, TrackingPixel, TrackingPixelInput } from './types'

type Problem = { detail?: string }
type ApiResult<T> = Promise<{ data?: T; error?: Problem; response: Response }>
type Range = { from: string; to: string }
type LinksApi = {
  GET: {
    (path: '/api/private/sites/{site}/links', options: { params: { path: { site: number } } }): ApiResult<ShortLink[]>
    (path: '/api/private/links/{link}', options: { params: { path: { link: number } } }): ApiResult<ShortLink>
    (path: '/api/private/sites/{site}/tracking-pixels', options: { params: { path: { site: number } } }): ApiResult<TrackingPixel[]>
    (path: '/api/private/tracking-pixels/{id}', options: { params: { path: { id: number } } }): ApiResult<TrackingPixel>
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
      options: { params: { path: { site: number }; query: Range & { campaign: string } } },
    ): ApiResult<PixelStats>
  }
  POST: {
    (path: '/api/private/sites/{site}/links', options: { params: { path: { site: number } }; body: LinkInput }): ApiResult<ShortLink>
    (path: '/api/private/sites/{site}/tracking-pixels', options: { params: { path: { site: number } }; body: TrackingPixelInput }): ApiResult<TrackingPixel>
  }
  PATCH: {
    (path: '/api/private/links/{link}', options: { params: { path: { link: number } }; body: Partial<LinkInput> }): ApiResult<ShortLink>
    (path: '/api/private/tracking-pixels/{id}', options: { params: { path: { id: number } }; body: { name: string } }): ApiResult<TrackingPixel>
  }
  DELETE: {
    (path: '/api/private/links/{link}', options: { params: { path: { link: number } } }): ApiResult<never>
    (path: '/api/private/tracking-pixels/{id}', options: { params: { path: { id: number } } }): ApiResult<never>
  }
}

const linksApi = api as unknown as LinksApi

export const linkKeys = {
  all: (siteId: number) => ['links', siteId] as const,
  one: (siteId: number, id: number) => ['links', siteId, 'one', id] as const,
  stats: (id: number, range: Range) => ['links', 'stats', id, range] as const,
  pixel: (siteId: number, campaign: string) => ['links', siteId, 'pixel', campaign] as const,
  pixelStats: (siteId: number, range: Range, tag: string) => ['links', siteId, 'pixel-stats', range, tag] as const,
  trackingPixels: (siteId: number) => ['links', siteId, 'tracking-pixels'] as const,
  trackingPixel: (siteId: number, id: number) => ['links', siteId, 'tracking-pixels', id] as const,
}

function requireData<T>(result: { data?: T; error?: Problem; response: Response }, message: string): T {
  if (result.error) throw apiProblem(result.response, result.error, message)
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

export function usePixelStats(siteId: number, range: Range, tag: string) {
  return useQuery<PixelStats, Error>({
    queryKey: linkKeys.pixelStats(siteId, range, tag),
    queryFn: async () =>
      requireData(
        await linksApi.GET('/api/private/sites/{site}/pixel/stats', {
          params: { path: { site: siteId }, query: { ...range, campaign: tag } },
        }),
        'Could not load impression statistics.',
      ),
  })
}

export function useTrackingPixels(siteId: number) {
  return useQuery<TrackingPixel[], Error>({
    queryKey: linkKeys.trackingPixels(siteId),
    queryFn: async () => requireData(
      await linksApi.GET('/api/private/sites/{site}/tracking-pixels', { params: { path: { site: siteId } } }),
      'Could not load tracking pixels.',
    ),
  })
}

export function useTrackingPixel(siteId: number, id: number) {
  return useQuery<TrackingPixel, Error>({
    queryKey: linkKeys.trackingPixel(siteId, id),
    queryFn: async () => requireData(
      await linksApi.GET('/api/private/tracking-pixels/{id}', { params: { path: { id } } }),
      'Could not load the tracking pixel.',
    ),
  })
}

export function useSaveTrackingPixel(siteId: number) {
  const client = useQueryClient()
  return useMutation<TrackingPixel, Error, { id?: number; input: TrackingPixelInput }>({
    mutationFn: async ({ id, input }) => id === undefined
      ? requireData(
          await linksApi.POST('/api/private/sites/{site}/tracking-pixels', { params: { path: { site: siteId } }, body: input }),
          'Could not create the tracking pixel.',
        )
      : requireData(
          await linksApi.PATCH('/api/private/tracking-pixels/{id}', { params: { path: { id } }, body: { name: input.name } }),
          'Could not rename the tracking pixel.',
        ),
    onSuccess: (pixel) => {
      client.setQueryData(linkKeys.trackingPixel(siteId, pixel.id), pixel)
      void client.invalidateQueries({ queryKey: linkKeys.trackingPixels(siteId) })
    },
  })
}

export function useDeleteTrackingPixel(siteId: number) {
  const client = useQueryClient()
  return useMutation<void, Error, number>({
    mutationFn: async (id) => {
      const result = await linksApi.DELETE('/api/private/tracking-pixels/{id}', { params: { path: { id } } })
      if (result.error) throw apiProblem(result.response, result.error, 'Could not delete the tracking pixel.')
    },
    onSuccess: (_data, id) => {
      client.removeQueries({ queryKey: linkKeys.trackingPixel(siteId, id) })
      void client.invalidateQueries({ queryKey: linkKeys.trackingPixels(siteId) })
    },
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
      if (result.error) throw apiProblem(result.response, result.error, 'Could not delete the link.')
    },
    onSuccess: () => client.invalidateQueries({ queryKey: linkKeys.all(siteId) }),
  })
}
