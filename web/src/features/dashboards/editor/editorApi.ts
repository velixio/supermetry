import { useMutation, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api'
import type { components } from '@/lib/api.gen'

import { dashboardKeys, type Dashboard, type DashboardLayout } from '../api'

export type Problem = components['schemas']['Problem']

/** The server answers 409 for a stale version, a taken name and the 50 dashboard limit. */
export type ConflictKind = 'stale' | 'name' | 'limit'

export function conflictKind(problem: Problem | undefined): ConflictKind | undefined {
  if (problem?.status !== 409) return undefined
  const detail = (problem.detail ?? '').toLowerCase()
  if (detail.includes('changed since')) return 'stale'
  if (detail.includes('at most')) return 'limit'
  return 'name'
}

/** Field messages the server attached to a 400, or its detail when there are none. */
export function problemMessages(problem: Problem | undefined): string[] {
  if (!problem) return []
  const fields = (problem.errors ?? []).map((item) => (item.field ? `${item.field}: ${item.message}` : item.message))
  if (fields.length > 0) return fields
  return problem.detail ? [problem.detail] : []
}

function useRefresh(siteId: number) {
  const client = useQueryClient()
  return () => client.invalidateQueries({ queryKey: dashboardKeys.list(siteId) })
}

function unwrap<T>(result: { data?: T; error?: Problem }): T {
  if (result.error) throw result.error
  if (result.data === undefined) throw new Error('The dashboards response was empty.')
  return result.data
}

export function useCreateDashboard(siteId: number) {
  const refresh = useRefresh(siteId)
  return useMutation<Dashboard, Problem, { name: string; layout: DashboardLayout }>({
    mutationFn: async (body) => unwrap(await api.POST('/api/private/sites/{site}/dashboards', { params: { path: { site: siteId } }, body })),
    onSuccess: refresh,
  })
}

export function useSaveDashboard(siteId: number) {
  const refresh = useRefresh(siteId)
  return useMutation<Dashboard, Problem, { id: number; name: string; layout: DashboardLayout; version: number }>({
    mutationFn: async ({ id, ...body }) =>
      unwrap(await api.PATCH('/api/private/sites/{site}/dashboards/{dashboard}', { params: { path: { site: siteId, dashboard: id } }, body })),
    onSuccess: refresh,
  })
}

export function useDeleteDashboard(siteId: number) {
  const refresh = useRefresh(siteId)
  return useMutation<void, Problem, number>({
    mutationFn: async (id) => {
      const { error } = await api.DELETE('/api/private/sites/{site}/dashboards/{dashboard}', { params: { path: { site: siteId, dashboard: id } } })
      if (error) throw error
    },
    onSuccess: refresh,
  })
}

export function useDuplicateDashboard(siteId: number) {
  const refresh = useRefresh(siteId)
  return useMutation<Dashboard, Problem, number>({
    mutationFn: async (id) =>
      unwrap(await api.POST('/api/private/sites/{site}/dashboards/{dashboard}/duplicate', { params: { path: { site: siteId, dashboard: id } } })),
    onSuccess: refresh,
  })
}

/** Flags a dashboard as the site default, or clears the flag when `id` is undefined. */
export function useSetDefaultDashboard(siteId: number) {
  const refresh = useRefresh(siteId)
  return useMutation<void, Problem, number | undefined>({
    mutationFn: async (id) => {
      const result = id === undefined
        ? await api.DELETE('/api/private/sites/{site}/dashboards/default', { params: { path: { site: siteId } } })
        : await api.POST('/api/private/sites/{site}/dashboards/{dashboard}/default', { params: { path: { site: siteId, dashboard: id } } })
      if (result.error) throw result.error
    },
    onSuccess: refresh,
  })
}
