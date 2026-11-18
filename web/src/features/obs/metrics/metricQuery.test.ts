import { describe, expect, it } from 'vitest'

import {
  buildMetricQuery, dateRangeInstants, defaultInterval, fitInterval, initialState, seriesLabel, toWidgetQuery, zonedDayStart,
} from './metricQuery'

const now = new Date('2026-11-18T12:00:00Z')

describe('buildMetricQuery', () => {
  it('turns the explorer state into the request body', () => {
    const query = buildMetricQuery({
      ...initialState, name: 'http.requests', agg: 'sum', groupBy: ['service', 'service', 'region', 'extra'],
      filters: [{ key: 'env', value: 'prod' }, { key: '', value: 'x' }, { key: 'host', value: '' }], range: '6h', interval: 'auto',
    }, now)
    expect(query).toEqual({
      name: 'http.requests', agg: 'sum', group_by: ['service', 'region'], filters: { env: 'prod' },
      from: '2026-11-18T06:00:00.000Z', to: '2026-11-18T12:00:00.000Z', interval: '1m',
    })
  })

  it('omits group_by and filters when empty', () => {
    const query = buildMetricQuery({ ...initialState, name: 'm', agg: 'avg' }, now)
    expect(query).not.toHaveProperty('group_by')
    expect(query).not.toHaveProperty('filters')
    expect(query.interval).toBe('5m')
  })
})

describe('intervals', () => {
  it('picks a readable step per range', () => {
    expect(defaultInterval(60 * 60_000)).toBe('1m')
    expect(defaultInterval(24 * 3_600_000)).toBe('5m')
    expect(defaultInterval(7 * 86_400_000)).toBe('1h')
    expect(defaultInterval(90 * 86_400_000)).toBe('1d')
  })

  it('keeps within the server limits: raw steps up to 48 h, 1,500 buckets', () => {
    expect(fitInterval('1m', 7 * 86_400_000)).toBe('1h')
    expect(fitInterval('1h', 90 * 86_400_000)).toBe('1d')
    expect(fitInterval('1m', 24 * 3_600_000)).toBe('1m')
  })
})

describe('time zones', () => {
  it('finds local midnight, including a +05:45 zone', () => {
    expect(zonedDayStart('2026-11-18', 'Asia/Kathmandu').toISOString()).toBe('2026-11-17T18:15:00.000Z')
    expect(zonedDayStart('2026-11-18', 'Asia/Kolkata').toISOString()).toBe('2026-11-17T18:30:00.000Z')
    expect(zonedDayStart('2026-11-18', 'UTC').toISOString()).toBe('2026-11-18T00:00:00.000Z')
  })

  it('converts an inclusive date range to instants', () => {
    expect(dateRangeInstants({ from: '2026-11-17', to: '2026-11-18' }, 'Asia/Kathmandu')).toEqual({
      from: '2026-11-16T18:15:00.000Z', to: '2026-11-18T18:15:00.000Z',
    })
  })
})

describe('widget queries and labels', () => {
  it('drops the time range for a widget', () => {
    const query = buildMetricQuery({ ...initialState, name: 'm', agg: 'sum' }, now)
    expect(toWidgetQuery(query)).toEqual({ name: 'm', agg: 'sum', interval: '5m' })
  })

  it('labels series by group values', () => {
    expect(seriesLabel({ group: {}, points: [] }, 'cpu')).toBe('cpu')
    expect(seriesLabel({ group: { service: 'api', region: 'eu' }, points: [] }, 'cpu')).toBe('service=api, region=eu')
  })
})
