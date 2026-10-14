import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api'
import { apiProblem } from '@/lib/apiProblem'

import type { Annotation, AnnotationInput } from './types'

type Problem = { detail?: string }
type ApiResult<T> = Promise<{ data?: T; error?: Problem; response: Response }>
type AnnotationApi = {
  GET: (
    path: '/api/private/sites/{site}/annotations',
    options: { params: { path: { site: number }; query: { from: string; to: string } } },
  ) => ApiResult<Annotation[]>
  POST: (
    path: '/api/private/sites/{site}/annotations',
    options: { params: { path: { site: number } }; body: AnnotationInput },
  ) => ApiResult<Annotation>
  PATCH: (
    path: '/api/private/annotations/{id}',
    options: { params: { path: { id: number } }; body: AnnotationInput },
  ) => ApiResult<Annotation>
  DELETE: (
    path: '/api/private/annotations/{id}',
    options: { params: { path: { id: number } } },
  ) => ApiResult<never>
}

const annotationApi = api as unknown as AnnotationApi

export const annotationKeys = {
  all: (siteId: number) => ['annotations', siteId] as const,
  range: (siteId: number, from: string, to: string) =>
    [...annotationKeys.all(siteId), { from, to }] as const,
}

function requireData<T>(result: { data?: T; error?: Problem; response?: Response }, message: string): T {
  if (result.error) throw result.response ? apiProblem(result.response, result.error, message) : new Error(result.error.detail ?? message)
  if (result.data === undefined) throw new Error(message)
  return result.data
}

export function useAnnotations(siteId: number, from: string, to: string) {
  return useQuery<Annotation[], Error>({
    queryKey: annotationKeys.range(siteId, from, to),
    queryFn: async () => {
      const result = await annotationApi.GET('/api/private/sites/{site}/annotations', {
        params: { path: { site: siteId }, query: { from, to } },
      })
      const data = requireData(result, 'Could not load annotations.')
      return Array.isArray(data) ? data : []
    },
  })
}

export function useCreateAnnotation(siteId: number) {
  const client = useQueryClient()
  return useMutation<Annotation, Error, AnnotationInput>({
    mutationFn: async (body) => requireData(
      await annotationApi.POST('/api/private/sites/{site}/annotations', {
        params: { path: { site: siteId } },
        body,
      }),
      'Could not create the annotation.',
    ),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: annotationKeys.all(siteId) })
    },
  })
}

export function useUpdateAnnotation(siteId: number) {
  const client = useQueryClient()
  return useMutation<Annotation, Error, { id: number; input: AnnotationInput }>({
    mutationFn: async ({ id, input }) => requireData(
      await annotationApi.PATCH('/api/private/annotations/{id}', {
        params: { path: { id } },
        body: input,
      }),
      'Could not update the annotation.',
    ),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: annotationKeys.all(siteId) })
    },
  })
}

export function useDeleteAnnotation(siteId: number) {
  const client = useQueryClient()
  return useMutation<void, Error, number>({
    mutationFn: async (id) => {
      const result = await annotationApi.DELETE('/api/private/annotations/{id}', {
        params: { path: { id } },
      })
      if (result.error) throw apiProblem(result.response, result.error, 'Could not delete the annotation.')
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: annotationKeys.all(siteId) })
    },
  })
}
