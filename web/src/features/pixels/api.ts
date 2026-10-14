import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { privateRequest } from '@/features/settings/privateApi'
import type { components } from '@/lib/api.gen'

export type Pixel = components['schemas']['Pixel']
export type PixelStats = components['schemas']['PixelStats']
export type PixelInput = components['schemas']['PixelInput']

export const usePixels = (siteId: number) =>
  useQuery({ queryKey: ['pixels', siteId], queryFn: () => privateRequest<Pixel[]>('GET', `/api/private/sites/${siteId}/pixels`) })

export const usePixelStats = (siteId: number, range: { from: string; to: string; pixel?: number }) =>
  useQuery({
    queryKey: ['pixel-stats', siteId, range.from, range.to, range.pixel ?? 0],
    queryFn: () => {
      const query = new URLSearchParams({ from: range.from, to: range.to })
      if (range.pixel) query.set('pixel', String(range.pixel))
      return privateRequest<PixelStats>('GET', `/api/private/sites/${siteId}/pixels/stats?${query}`)
    },
  })

function useInvalidate(siteId: number) {
  const client = useQueryClient()
  return () => Promise.all([
    client.invalidateQueries({ queryKey: ['pixels', siteId] }),
    client.invalidateQueries({ queryKey: ['pixel-stats', siteId] }),
  ])
}

export function useCreatePixel(siteId: number) {
  const invalidate = useInvalidate(siteId)
  return useMutation({ mutationFn: (body: PixelInput) => privateRequest<Pixel>('POST', `/api/private/sites/${siteId}/pixels`, { body }), onSuccess: invalidate })
}

export function useUpdatePixel(siteId: number) {
  const invalidate = useInvalidate(siteId)
  return useMutation({ mutationFn: ({ id, ...body }: PixelInput & { id: number }) => privateRequest<Pixel>('PATCH', `/api/private/sites/${siteId}/pixels/${id}`, { body }), onSuccess: invalidate })
}

export function useDeletePixel(siteId: number) {
  const invalidate = useInvalidate(siteId)
  return useMutation({ mutationFn: (id: number) => privateRequest<void>('DELETE', `/api/private/sites/${siteId}/pixels/${id}`), onSuccess: invalidate })
}
