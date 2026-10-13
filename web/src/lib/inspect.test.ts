import { describe, expect, it } from 'vitest'

import {
  activeInspectTarget,
  inspectAllowed,
  inspectCrossDimensions,
  inspectFilter,
  inspectTargetFor,
  nextInspect,
  parseInspect,
  parseInspectSearch,
  sameInspect,
  serializeInspect,
  withoutInspect,
  type InspectTarget,
} from './inspect'

const india: InspectTarget = { dimension: 'country', values: ['IN'] }

describe('inspect search value', () => {
  it.each([
    ['country:IN', { dimension: 'country', values: ['IN'] }],
    ['page:%2Fpricing%2Cplans%3Aannual', { dimension: 'page', values: ['/pricing,plans:annual'] }],
    ['city:Springfield,US', { dimension: 'city', values: ['Springfield', 'US'] }],
    ['weekday_hour:1,13', { dimension: 'weekday_hour', values: ['1', '13'] }],
    ['screen_size:mobile', { dimension: 'screen_size', values: ['mobile'] }],
  ])('parses and re-serialises %s', (raw, target) => {
    expect(parseInspect(raw)).toEqual(target)
    expect(serializeInspect(parseInspect(raw)!)).toBe(raw)
  })

  it.each([
    'nope:1', 'prop:plan:pro', 'time:2026-09-28', 'country:', ':IN', 'country', 'country:IN,DE',
    'city:Springfield', 'city:Springfield,usa', 'region:,US', 'weekday_hour:7,1', 'weekday_hour:1,24',
    'weekday_hour:1', 'weekday_hour:a,1', 'page:%E0%A4%A', 42, undefined, null,
  ])('drops %s', (raw) => {
    expect(parseInspect(raw)).toBeUndefined()
  })

  it('keeps only a valid value when reading route search', () => {
    expect(parseInspectSearch({ period: '7d', inspect: 'country:IN' })).toEqual({ inspect: 'country:IN' })
    expect(parseInspectSearch({ inspect: 'nope:1' })).toEqual({})
    expect(parseInspectSearch(undefined)).toEqual({})
    expect(withoutInspect({ period: '7d', inspect: 'country:IN' })).toEqual({ period: '7d' })
  })

  it('builds targets only for supported values', () => {
    expect(inspectTargetFor('region', ['California', 'US'])).toEqual({ dimension: 'region', values: ['California', 'US'] })
    expect(inspectTargetFor('region', ['California', ''])).toBeUndefined()
    expect(inspectTargetFor('prop:plan', ['pro'])).toBeUndefined()
    expect(inspectTargetFor('country', [''])).toBeUndefined()
  })

  it('toggles the same selection closed and replaces a different one', () => {
    expect(sameInspect(india, { dimension: 'country', values: ['IN'] })).toBe(true)
    expect(sameInspect(india, undefined)).toBe(false)
    expect(nextInspect(undefined, india)).toBe('country:IN')
    expect(nextInspect(india, india)).toBeUndefined()
    expect(nextInspect(india, { dimension: 'country', values: ['DE'] })).toBe('country:DE')
  })

  it('turns a value into the same is filter a row click adds', () => {
    expect(inspectFilter({ dimension: 'city', values: ['Springfield', 'US'] })).toEqual({ dimension: 'city', operator: 'is', values: ['Springfield', 'US'] })
    expect(inspectFilter({ dimension: 'weekday_hour', values: ['1', '13'] })).toBeUndefined()
  })
})

describe('inspect privacy and availability', () => {
  it.each([
    [india, false, true],
    [india, true, true],
    [{ dimension: 'country', values: ['ZZ'] } as InspectTarget, true, false],
    [{ dimension: 'page', values: ['/'] } as InspectTarget, true, false],
    [{ dimension: 'page', values: ['/'] } as InspectTarget, false, true],
    [{ dimension: 'source', values: ['news.example'] } as InspectTarget, true, false],
    [{ dimension: 'channel', values: ['Organic Search'] } as InspectTarget, true, true],
    [{ dimension: 'weekday_hour', values: ['1', '13'] } as InspectTarget, true, true],
  ])('inspectAllowed(%j, anonymous=%s) is %s', (target, anonymous, allowed) => {
    expect(inspectAllowed(target, anonymous)).toBe(allowed)
  })

  it('applies only on the standard dashboard and only when allowed', () => {
    expect(activeInspectTarget('country:IN', { anonymous: false, standardDashboard: true })).toEqual(india)
    expect(activeInspectTarget('country:IN', { anonymous: false, standardDashboard: false })).toBeUndefined()
    expect(activeInspectTarget('page:%2F', { anonymous: true, standardDashboard: true })).toBeUndefined()
    expect(activeInspectTarget('nope:1', { anonymous: false, standardDashboard: true })).toBeUndefined()
  })

  it.each([
    [india, false, ['page', 'source', 'device']],
    [{ dimension: 'city', values: ['Springfield', 'US'] } as InspectTarget, false, ['page', 'source', 'device']],
    [{ dimension: 'entry', values: ['/'] } as InspectTarget, false, ['source', 'country', 'device']],
    [{ dimension: 'page', values: ['/'] } as InspectTarget, false, ['source', 'country', 'device']],
    [india, true, ['channel', 'device']],
    [{ dimension: 'channel', values: ['Email'] } as InspectTarget, true, ['country', 'device']],
    [{ dimension: 'weekday_hour', values: ['1', '13'] } as InspectTarget, false, []],
  ])('cross-breaks %j (anonymous=%s) by %j', (target, anonymous, dimensions) => {
    expect(inspectCrossDimensions(target, anonymous)).toEqual(dimensions)
  })
})
