import { describe, expect, it } from 'vitest'

import { parseSearch, stringifySearch } from '@/router'
import { parseReplaySearch, readReplayLayout, rememberReplayLayout, replayFilterSearch } from './search'

describe('replay URL search', () => {
  it('round trips every triage filter and layout while preserving dashboard state', () => {
    const parsed = parseReplaySearch({
      period: 'custom', from: '2026-10-01', to: '2026-10-08', q: '  checkout  ',
      min_duration: '30', max_duration: 90, has_events: 'false', saved: 'true', layout: 'cards',
      page: '3', cursors: ['one', 'two'], replay: '401', unknown: 'drop-me',
    })

    expect(parsed).toMatchObject({
      period: 'custom', from: '2026-10-01', to: '2026-10-08', q: 'checkout',
      min_duration: 30, max_duration: 90, has_events: false, saved: true, layout: 'cards',
      page: 3, cursors: ['one', 'two'], replay: '401',
    })
    expect(parsed).not.toHaveProperty('unknown')
    expect(parseReplaySearch(parseSearch(stringifySearch(parsed)))).toEqual(parsed)
  })

  it('drops invalid and over-limit filter values', () => {
    const parsed = parseReplaySearch({
      q: 'x'.repeat(201), min_duration: -1, max_duration: 31_536_001,
      has_events: 'yes', saved: 1, layout: 'grid',
    })
    expect(parsed).not.toHaveProperty('q')
    expect(parsed).not.toHaveProperty('min_duration')
    expect(parsed).not.toHaveProperty('max_duration')
    expect(parsed).not.toHaveProperty('has_events')
    expect(parsed).not.toHaveProperty('saved')
    expect(parsed).not.toHaveProperty('layout')
  })

  it('clears cursor history when any filter changes but keeps an open replay', () => {
    expect(replayFilterSearch({ page: 4, cursors: ['a', 'b', 'c'], replay: '401', q: 'old' }, { q: 'new' })).toMatchObject({
      page: 1, cursors: [], replay: '401', q: 'new',
    })
  })

  it('remembers layout independently for each signed-in user', () => {
    localStorage.clear()
    rememberReplayLayout(10, 'cards')
    rememberReplayLayout(11, 'table')
    expect(readReplayLayout(10)).toBe('cards')
    expect(readReplayLayout(11)).toBe('table')
    expect(readReplayLayout(12)).toBe('table')
  })
})
