import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api'
import type { components } from '@/lib/api.gen'

import type { FunnelCountBy, FunnelResult, FunnelStep, FunnelWindow, SavedFunnel } from './types'

type Problem = components['schemas']['Problem']
type ApiResult<T> = Promise<{ data?: T; error?: Problem; response: Response }>
type FunnelApi = {
  GET: (
    path: '/api/private/sites/{site_id}/funnels',
    options: { params: { path: { site_id: number } } },
  ) => ApiResult<SavedFunnel[]>
  POST: {
    (
      path: '/api/private/sites/{site_id}/funnels',
      options: { params: { path: { site_id: number } }; body: { name: string; steps: FunnelStep[] } },
    ): ApiResult<SavedFunnel>
    (
      path: '/api/private/funnels/query',
      options: { body: { site_id: number; date_range: { from: string; to: string }; steps: FunnelStep[]; count_by: FunnelCountBy; window: FunnelWindow } },
    ): ApiResult<FunnelResult>
  }
  PATCH: (
    path: '/api/private/funnels/{funnel_id}',
    options: { params: { path: { funnel_id: number } }; body: { name: string; steps: FunnelStep[] } },
  ) => ApiResult<SavedFunnel>
}

const funnelApi = api as unknown as FunnelApi

export const funnelKeys = {
  all: (siteId: number) => ['funnels', siteId] as const,
  result: (siteId: number, steps: FunnelStep[], from: string, to: string, countBy: FunnelCountBy, window: FunnelWindow) =>
    ['funnels', siteId, 'result', steps, from, to, countBy, window] as const,
}

export function useFunnels(siteId: number) {
  return useQuery<SavedFunnel[], Problem>({
    queryKey: funnelKeys.all(siteId),
    queryFn: async () => {
      const { data, error } = await funnelApi.GET(
        '/api/private/sites/{site_id}/funnels',
        { params: { path: { site_id: siteId } } },
      )
      if (error) throw error
      return data ?? []
    },
  })
}

export function useCreateFunnel(siteId: number) {
  const client = useQueryClient()
  return useMutation<SavedFunnel, Problem, { name: string; steps: FunnelStep[] }>({
    mutationFn: async (body) => {
      const { data, error } = await funnelApi.POST(
        '/api/private/sites/{site_id}/funnels',
        { params: { path: { site_id: siteId } }, body },
      )
      if (error) throw error
      if (!data) throw new Error('The funnel response was empty.')
      return data
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: funnelKeys.all(siteId) })
    },
  })
}

export function useUpdateFunnel(siteId: number) {
  const client = useQueryClient()
  return useMutation<SavedFunnel, Problem, { id: number; name: string; steps: FunnelStep[] }>({
    mutationFn: async ({ id, name, steps }) => {
      const { data, error } = await funnelApi.PATCH('/api/private/funnels/{funnel_id}', {
        params: { path: { funnel_id: id } },
        body: { name, steps },
      })
      if (error) throw error
      if (!data) throw new Error('The funnel response was empty.')
      return data
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: funnelKeys.all(siteId) })
    },
  })
}

export function useFunnelResult(
  siteId: number,
  dateRange: { from: string; to: string },
  steps: FunnelStep[],
  countBy: FunnelCountBy,
  window: FunnelWindow,
) {
  return useQuery<FunnelResult, Problem>({
    queryKey: funnelKeys.result(siteId, steps, dateRange.from, dateRange.to, countBy, window),
    enabled: steps.length >= 2 && steps.every((step) => step.value.trim() !== ''),
    queryFn: async () => {
      const { data, error } = await funnelApi.POST('/api/private/funnels/query', {
        body: { site_id: siteId, date_range: dateRange, steps, count_by: countBy, window },
      })
      if (error) throw error
      if (!data) throw new Error('The funnel result was empty.')
      return data
    },
  })
}
