import type { components } from '@/lib/api.gen'

export type Cohort = components['schemas']['Cohort']
export type CohortCondition = components['schemas']['CohortCondition']
export type CohortDefinition = components['schemas']['CohortDefinition']
export type CohortInput = components['schemas']['CohortInput']
export type CohortEvaluation = components['schemas']['CohortEvaluation']
export type CohortMember = components['schemas']['CohortMember']
export type PowerUsers = components['schemas']['PowerUsers']
export type PowerUser = components['schemas']['PowerUser']

export type ConditionType = CohortCondition['type']
export type PowerMetric = 'sessions' | 'events'

export const maxConditions = 10
export const maxWindowDays = 90
export const thresholdChoices = [1, 2, 3, 5, 10, 25, 50, 100] as const
