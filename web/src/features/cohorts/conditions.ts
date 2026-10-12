import { en } from '@/messages/en'

import { maxConditions, maxWindowDays, type CohortCondition, type CohortDefinition, type ConditionType } from './types'

const t = en.cohorts

// Drafts keep every input as text so the form can hold half-typed values; they
// become an API definition only through toDefinition, which reports problems.
export type DraftCondition = {
  key: number
  type: ConditionType
  negate: boolean
  name: string
  path: string
  operator: 'is' | 'starts_with'
  propKey: string
  propValue: string
  metric: 'sessions' | 'events'
  minCount: string
  relation: 'within' | 'before'
  days: string
}

export type DraftCohort = {
  name: string
  match: 'all' | 'any'
  windowDays: string
  conditions: DraftCondition[]
}

let nextKey = 1

export function blankCondition(type: ConditionType = 'event'): DraftCondition {
  return {
    key: nextKey++, type, negate: false, name: '', path: '', operator: 'is', propKey: '', propValue: '',
    metric: 'sessions', minCount: '1', relation: 'within', days: '30',
  }
}

export function blankDraft(): DraftCohort {
  return { name: '', match: 'all', windowDays: '30', conditions: [blankCondition()] }
}

export function draftFromDefinition(name: string, definition: CohortDefinition): DraftCohort {
  return {
    name,
    match: definition.match === 'any' ? 'any' : 'all',
    windowDays: String(definition.window_days),
    conditions: definition.conditions.map((condition) => ({
      ...blankCondition(condition.type),
      negate: condition.negate === true,
      name: condition.name ?? '',
      path: condition.path ?? '',
      operator: condition.operator === 'starts_with' ? 'starts_with' : 'is',
      propKey: condition.key ?? '',
      propValue: condition.value ?? '',
      metric: condition.metric === 'events' ? 'events' : 'sessions',
      minCount: String(condition.min_count ?? 1),
      relation: condition.relation === 'before' ? 'before' : 'within',
      days: String(condition.days ?? 30),
    })),
  }
}

function wholeNumber(text: string, min: number, max: number): number | undefined {
  if (!/^\d+$/.test(text.trim())) return undefined
  const value = Number(text)
  return value >= min && value <= max ? value : undefined
}

export type DraftResult = { definition?: CohortDefinition; errors: string[] }

// toDefinition validates against the same limits the server enforces so the
// common mistakes are caught before a request is sent.
export function toDefinition(draft: DraftCohort): DraftResult {
  const errors: string[] = []
  const windowDays = wholeNumber(draft.windowDays, 1, maxWindowDays)
  if (windowDays === undefined) errors.push(t.errors.window)
  if (draft.conditions.length < 1) errors.push(t.errors.noConditions)
  if (draft.conditions.length > maxConditions) errors.push(t.errors.tooMany)
  const conditions: CohortCondition[] = []
  draft.conditions.forEach((condition, index) => {
    const label = t.conditionLabel(index + 1)
    const base: CohortCondition = { type: condition.type, negate: condition.negate }
    const text = (value: string, field: string, max: number): string | undefined => {
      const trimmed = value.trim()
      if (trimmed === '') { errors.push(`${label}: ${field} ${t.errors.required}`); return undefined }
      if (Array.from(trimmed).length > max) { errors.push(`${label}: ${field} ${t.errors.tooLong(max)}`); return undefined }
      return trimmed
    }
    const count = (): number | undefined => {
      const value = wholeNumber(condition.minCount, 1, 1000)
      if (value === undefined) errors.push(`${label}: ${t.errors.count}`)
      return value
    }
    switch (condition.type) {
      case 'event': {
        const name = text(condition.name, t.fields.eventName, 200)
        const min = count()
        if (name !== undefined && min !== undefined) conditions.push({ ...base, name, min_count: min })
        break
      }
      case 'event_property': {
        const name = text(condition.name, t.fields.eventName, 200)
        const key = text(condition.propKey, t.fields.propertyKey, 100)
        const value = text(condition.propValue, t.fields.propertyValue, 200)
        const min = count()
        if (name !== undefined && key !== undefined && value !== undefined && min !== undefined) {
          conditions.push({ ...base, name, key, value, min_count: min })
        }
        break
      }
      case 'page': {
        const path = text(condition.path, t.fields.pagePath, 500)
        const min = count()
        if (path !== undefined && min !== undefined) conditions.push({ ...base, path, operator: condition.operator, min_count: min })
        break
      }
      case 'activity': {
        const min = count()
        if (min !== undefined) conditions.push({ ...base, metric: condition.metric, min_count: min })
        break
      }
      default: {
        const days = wholeNumber(condition.days, 1, 365)
        if (days === undefined) errors.push(`${label}: ${t.errors.days}`)
        else conditions.push({ ...base, relation: condition.relation, days })
      }
    }
  })
  if (errors.length > 0 || windowDays === undefined) return { errors }
  return { errors, definition: { match: draft.match, window_days: windowDays, conditions } }
}

export function describeCondition(condition: CohortCondition): string {
  const times = (count: number | undefined) => (count && count > 1 ? t.times(count) : '')
  const prefix = condition.negate ? t.not : ''
  switch (condition.type) {
    case 'event':
      return prefix + t.describe.event(condition.name ?? '', times(condition.min_count))
    case 'event_property':
      return prefix + t.describe.property(condition.name ?? '', condition.key ?? '', condition.value ?? '', times(condition.min_count))
    case 'page':
      return prefix + (condition.operator === 'starts_with'
        ? t.describe.pagePrefix(condition.path ?? '', times(condition.min_count))
        : t.describe.page(condition.path ?? '', times(condition.min_count)))
    case 'activity':
      return prefix + t.describe.activity(condition.metric === 'events' ? t.metricEvents : t.metricSessions, condition.min_count ?? 1)
    case 'first_seen':
      return prefix + t.describe.seen(t.firstSeen, condition.relation === 'before' ? t.before : t.within, condition.days ?? 0)
    default:
      return prefix + t.describe.seen(t.lastSeen, condition.relation === 'before' ? t.before : t.within, condition.days ?? 0)
  }
}

export function describeDefinition(definition: CohortDefinition): string {
  const joiner = definition.match === 'any' ? t.joinAny : t.joinAll
  return definition.conditions.map(describeCondition).join(joiner)
}
