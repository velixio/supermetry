import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api'
import type { components } from '@/lib/api.gen'

import type { Goal, GoalInput } from './types'

type Problem = components['schemas']['Problem']
type ApiResult<T> = Promise<{ data?: T; error?: Problem; response: Response }>
type GoalApi = {
  GET: (
    path: '/api/private/sites/{site_id}/goals',
    options: { params: { path: { site_id: number } } },
  ) => ApiResult<Goal[]>
  POST: (
    path: '/api/private/sites/{site_id}/goals',
    options: { params: { path: { site_id: number } }; body: GoalInput },
  ) => ApiResult<Goal>
  PATCH: (
    path: '/api/private/goals/{goal_id}',
    options: { params: { path: { goal_id: number } }; body: GoalInput },
  ) => ApiResult<Goal>
}

const goalApi = api as unknown as GoalApi
export const goalKeys = { all: (siteId: number) => ['goals', siteId] as const }

export function useGoals(siteId: number) {
  return useQuery<Goal[], Problem>({
    queryKey: goalKeys.all(siteId),
    queryFn: async () => {
      const { data, error } = await goalApi.GET('/api/private/sites/{site_id}/goals', {
        params: { path: { site_id: siteId } },
      })
      if (error) throw error
      return data ?? []
    },
  })
}

export function useSaveGoal(siteId: number) {
  const client = useQueryClient()
  return useMutation<Goal, Problem, { id?: number; input: GoalInput }>({
    mutationFn: async ({ id, input }) => {
      const result = id === undefined
        ? await goalApi.POST('/api/private/sites/{site_id}/goals', {
            params: { path: { site_id: siteId } },
            body: input,
          })
        : await goalApi.PATCH('/api/private/goals/{goal_id}', {
            params: { path: { goal_id: id } },
            body: input,
          })
      if (result.error) throw result.error
      if (!result.data) throw new Error('The goal response was empty.')
      return result.data
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: goalKeys.all(siteId) })
    },
  })
}
