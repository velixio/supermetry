import { describe, expect, it } from 'vitest'

import { blankCondition, blankDraft, describeCondition, describeDefinition, draftFromDefinition, toDefinition } from './conditions'

describe('cohort drafts', () => {
  it('turns a filled draft into the API definition with trimmed text and numbers', () => {
    const draft = blankDraft()
    draft.name = 'Pricing fans'
    draft.windowDays = '14'
    draft.match = 'any'
    draft.conditions = [
      { ...blankCondition('event'), name: ' signup ', minCount: '2' },
      { ...blankCondition('page'), path: '/docs', operator: 'starts_with', negate: true },
      { ...blankCondition('event_property'), name: 'purchase', propKey: 'plan', propValue: 'pro' },
      { ...blankCondition('activity'), metric: 'events', minCount: '5' },
      { ...blankCondition('last_seen'), relation: 'before', days: '30' },
    ]
    const result = toDefinition(draft)
    expect(result.errors).toEqual([])
    expect(result.definition).toEqual({
      match: 'any',
      window_days: 14,
      conditions: [
        { type: 'event', negate: false, name: 'signup', min_count: 2 },
        { type: 'page', negate: true, path: '/docs', operator: 'starts_with', min_count: 1 },
        { type: 'event_property', negate: false, name: 'purchase', key: 'plan', value: 'pro', min_count: 1 },
        { type: 'activity', negate: false, metric: 'events', min_count: 5 },
        { type: 'last_seen', negate: false, relation: 'before', days: 30 },
      ],
    })
  })

  it('reports every out-of-bounds field instead of sending a request', () => {
    const draft = blankDraft()
    draft.windowDays = '91'
    draft.conditions = [
      { ...blankCondition('event'), name: '', minCount: '0' },
      { ...blankCondition('first_seen'), days: '366' },
      { ...blankCondition('page'), path: 'x'.repeat(501) },
    ]
    const { errors, definition } = toDefinition(draft)
    expect(definition).toBeUndefined()
    expect(errors).toEqual(expect.arrayContaining([
      expect.stringContaining('window'),
      expect.stringContaining('Condition 1: Event name is required'),
      expect.stringContaining('Condition 1: the count'),
      expect.stringContaining('Condition 2: days'),
      expect.stringContaining('Condition 3: Page path must not exceed 500'),
    ]))
  })

  it('limits a cohort to ten conditions', () => {
    const draft = blankDraft()
    draft.conditions = Array.from({ length: 11 }, () => ({ ...blankCondition('activity') }))
    expect(toDefinition(draft).errors).toContain('A cohort can have at most 10 conditions.')
  })

  it('round-trips a stored definition through the editor', () => {
    const definition = { match: 'all' as const, window_days: 7, conditions: [{ type: 'page' as const, negate: false, path: '/pricing', operator: 'is' as const, min_count: 3 }] }
    expect(toDefinition(draftFromDefinition('x', definition)).definition).toEqual(definition)
  })

  it('describes conditions in plain language', () => {
    expect(describeCondition({ type: 'event', name: 'signup', min_count: 2 })).toBe('Did “signup” 2+ times')
    expect(describeCondition({ type: 'page', path: '/docs', operator: 'starts_with', negate: true })).toBe('Not: Viewed pages starting with /docs')
    expect(describeCondition({ type: 'activity', metric: 'sessions', min_count: 3 })).toBe('Has at least 3 sessions')
    expect(describeCondition({ type: 'first_seen', relation: 'within', days: 10 })).toBe('Profile first seen within the last 10 days')
    expect(describeDefinition({ match: 'any', window_days: 7, conditions: [{ type: 'event', name: 'a' }, { type: 'event', name: 'b' }] })).toBe('Did “a” or Did “b”')
  })
})
