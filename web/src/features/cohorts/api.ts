import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api'
import type { components } from '@/lib/api.gen'
import { apiProblem } from '@/lib/apiProblem'

import type { Cohort, CohortDefinition, CohortEvaluation, PowerMetric, PowerUsers } from './types'

type Problem = components['schemas']['Problem']

function failure(error: Problem | undefined, fallback: string, response?: Response): Error {
  return response ? apiProblem(response, error, fallback) : new Error(error?.detail ?? fallback)
}

export const cohortKeys = {
  site: (siteId: number) => ['sites', siteId, 'cohorts'] as const,
  evaluation: (siteId: number, subject: unknown) => ['sites', siteId, 'cohort-evaluation', subject] as const,
  power: (siteId: number, range: { from: string; to: string }, metric: PowerMetric, min: number) =>
    ['sites', siteId, 'power-users', range, metric, min] as const,
}

export function useCohorts(siteId: number, enabled = true) {
  return useQuery<Cohort[], Error>({
    queryKey: cohortKeys.site(siteId),
    enabled,
    queryFn: async ({ signal }) => {
      const { data, error, response } = await api.GET('/api/private/sites/{site}/cohorts', {
        params: { path: { site: siteId } },
        signal,
      })
      if (error) throw failure(error, 'Could not load cohorts.', response)
      return data?.data ?? []
    },
  })
}

export function useSaveCohort(siteId: number) {
  const client = useQueryClient()
  return useMutation<Cohort, Error, { id?: number; name: string; definition: CohortDefinition }>({
    mutationFn: async ({ id, name, definition }) => {
      const result = id === undefined
        ? await api.POST('/api/private/sites/{site}/cohorts', {
            params: { path: { site: siteId } },
            body: { name, definition },
          })
        : await api.PATCH('/api/private/cohorts/{cohort}', {
            params: { path: { cohort: id } },
            body: { name, definition },
          })
      if (result.error) throw failure(result.error as Problem, 'Could not save the cohort.', result.response)
      if (!result.data) throw new Error('The cohort response was empty.')
      return result.data as Cohort
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: cohortKeys.site(siteId), exact: true })
    },
  })
}

export function useDuplicateCohort(siteId: number) {
  const client = useQueryClient()
  return useMutation<Cohort, Error, number>({
    mutationFn: async (id) => {
      const { data, error, response } = await api.POST('/api/private/cohorts/{cohort}/duplicate', {
        params: { path: { cohort: id } },
      })
      if (error) throw failure(error as Problem, 'Could not duplicate the cohort.', response)
      if (!data) throw new Error('The cohort response was empty.')
      return data as Cohort
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: cohortKeys.site(siteId), exact: true })
    },
  })
}

export function useDeleteCohort(siteId: number) {
  const client = useQueryClient()
  return useMutation<void, Error, number>({
    mutationFn: async (id) => {
      const { error, response } = await api.DELETE('/api/private/cohorts/{cohort}', {
        params: { path: { cohort: id } },
      })
      if (error) throw failure(error as Problem, 'Could not delete the cohort.', response)
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: cohortKeys.site(siteId), exact: true })
    },
  })
}

export type EvaluationSubject =
  | { kind: 'saved'; id: number }
  | { kind: 'draft'; definition: CohortDefinition; nonce: number }

// Evaluation runs only when asked (enabled), never on page load: it scans
// retained events, so it is an explicit action with a bounded server cost.
export function useCohortEvaluation(siteId: number, subject: EvaluationSubject | undefined, sampleLimit = 10) {
  return useQuery<CohortEvaluation, Error>({
    queryKey: cohortKeys.evaluation(siteId, subject),
    enabled: subject !== undefined,
    staleTime: 0,
    gcTime: 60_000,
    retry: false,
    queryFn: async ({ signal }) => {
      if (!subject) throw new Error('Nothing to evaluate.')
      const body = subject.kind === 'saved'
        ? { cohort_id: subject.id, sample_limit: sampleLimit }
        : { definition: subject.definition, sample_limit: sampleLimit }
      const { data, error, response } = await api.POST('/api/private/sites/{site}/cohorts/evaluate', {
        params: { path: { site: siteId } },
        body,
        signal,
      })
      if (error) throw failure(error as Problem, 'Could not evaluate the cohort.', response)
      if (!data) throw new Error('The evaluation response was empty.')
      return data
    },
  })
}

export function usePowerUsers(
  siteId: number,
  range: { from: string; to: string },
  metric: PowerMetric,
  min: number,
  enabled = true,
) {
  return useQuery<PowerUsers, Error>({
    queryKey: cohortKeys.power(siteId, range, metric, min),
    enabled,
    retry: false,
    queryFn: async ({ signal }) => {
      const { data, error, response } = await api.GET('/api/private/sites/{site}/power-users', {
        params: { path: { site: siteId }, query: { from: range.from, to: range.to, metric, min, limit: 25 } },
        signal,
      })
      if (error) throw failure(error as Problem, 'Could not load power users.', response)
      if (!data) throw new Error('The power users response was empty.')
      return data
    },
  })
}
