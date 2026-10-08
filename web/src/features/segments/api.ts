import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api'
import type { components } from '@/lib/api.gen'

import type { Segment, SegmentInput } from './types'

type Problem = components['schemas']['Problem']
type ApiResult<T> = Promise<{ data?: T; error?: Problem; response: Response }>
type SegmentApi = {
  GET: (
    path: '/api/private/sites/{site_id}/segments',
    options: { params: { path: { site_id: number } } },
  ) => ApiResult<Segment[]>
  POST: (
    path: '/api/private/sites/{site_id}/segments',
    options: { params: { path: { site_id: number } }; body: SegmentInput },
  ) => ApiResult<Segment>
  PATCH: (
    path: '/api/private/segments/{segment_id}',
    options: { params: { path: { segment_id: number } }; body: SegmentInput },
  ) => ApiResult<Segment>
  DELETE: (
    path: '/api/private/segments/{segment_id}',
    options: { params: { path: { segment_id: number } } },
  ) => ApiResult<undefined>
}

const segmentApi = api as unknown as SegmentApi

export const segmentKeys = {
  site: (siteId: number) => ['sites', siteId, 'segments'] as const,
}

function apiError(error: Problem | undefined, fallback: string): Error {
  return new Error(error?.detail ?? fallback)
}

export function useSegments(siteId: number) {
  return useQuery<Segment[], Error>({
    queryKey: segmentKeys.site(siteId),
    queryFn: async () => {
      const { data, error } = await segmentApi.GET('/api/private/sites/{site_id}/segments', {
        params: { path: { site_id: siteId } },
      })
      if (error) throw apiError(error, 'Could not load segments.')
      return Array.isArray(data) ? data : []
    },
  })
}

export function useSaveSegment(siteId: number) {
  const client = useQueryClient()
  return useMutation<Segment, Error, { id?: number; input: SegmentInput }>({
    mutationFn: async ({ id, input }) => {
      const result = id === undefined
        ? await segmentApi.POST('/api/private/sites/{site_id}/segments', {
            params: { path: { site_id: siteId } },
            body: input,
          })
        : await segmentApi.PATCH('/api/private/segments/{segment_id}', {
            params: { path: { segment_id: id } },
            body: input,
          })
      if (result.error) throw apiError(result.error, 'Could not save the segment.')
      if (!result.data) throw new Error('The segment response was empty.')
      return result.data
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: segmentKeys.site(siteId), exact: true })
    },
  })
}

export function useDeleteSegment(siteId: number) {
  const client = useQueryClient()
  return useMutation<void, Error, number>({
    mutationFn: async (segmentId) => {
      const { error } = await segmentApi.DELETE('/api/private/segments/{segment_id}', {
        params: { path: { segment_id: segmentId } },
      })
      if (error) throw apiError(error, 'Could not delete the segment.')
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: segmentKeys.site(siteId), exact: true })
    },
  })
}
