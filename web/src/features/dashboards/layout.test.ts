import { describe, expect, it } from 'vitest'

import type { DashboardLayout, DashboardWidget } from './api'
import { dashKey, parseDashKey, readingOrder, widgetQuery } from './layout'

const widget = (id: string, x: number, y: number, extra: Partial<DashboardWidget> = {}): DashboardWidget => ({
  id, kind: 'goals', x, y, w: 6, h: 3, ...extra,
})

const base = { siteId: 3, dateRange: { from: '2000-01-01', to: '2000-01-01' }, metrics: ['visitors'], filters: [] } as never
const now = new Date('2026-10-12T12:00:00Z')

describe('dashboard layout helpers', () => {
  it('orders widgets top to bottom, then left to right, keeping ties stable', () => {
    const ordered = readingOrder([widget('c', 6, 0), widget('d', 0, 5), widget('a', 0, 0), widget('b', 0, 0)])
    expect(ordered.map((item) => item.id)).toEqual(['a', 'b', 'c', 'd'])
  })

  it('maps dashboard ids to URL keys and back', () => {
    expect(dashKey(0)).toBe('builtin')
    expect(dashKey(12)).toBe('12')
    expect(parseDashKey('12')).toBe(12)
    expect(parseDashKey('builtin')).toBeUndefined()
    expect(parseDashKey(undefined)).toBeUndefined()
    expect(parseDashKey('0')).toBeUndefined()
    expect(parseDashKey('-3')).toBeUndefined()
  })

  it('uses the layout period, then the URL period, and lets a widget override both', () => {
    const layout: DashboardLayout = { schema_version: 1, period: '7d', widgets: [] }
    const context = { base, timezone: 'UTC', now }
    expect(widgetQuery(layout, widget('a', 0, 0), [], context).dateRange).toEqual({ from: '2026-10-06', to: '2026-10-12' })
    expect(widgetQuery(layout, widget('a', 0, 0), [], { ...context, period: '28d' }).dateRange).toEqual({ from: '2026-09-15', to: '2026-10-12' })
    expect(widgetQuery(layout, widget('a', 0, 0, { period: 'today' }), [], { ...context, period: '28d' }).dateRange).toEqual({ from: '2026-10-12', to: '2026-10-12' })
  })

  it('applies a custom URL range only to widgets without their own period', () => {
    const layout: DashboardLayout = { schema_version: 1, widgets: [] }
    const context = { base, timezone: 'UTC', now, period: 'custom' as const, from: '2026-09-01', to: '2026-09-03' }
    expect(widgetQuery(layout, widget('a', 0, 0), [], context).dateRange).toEqual({ from: '2026-09-01', to: '2026-09-03' })
    expect(widgetQuery(layout, widget('a', 0, 0, { period: 'yesterday' }), [], context).dateRange).toEqual({ from: '2026-10-11', to: '2026-10-11' })
  })

  it('adds layout, URL and widget filters in that order', () => {
    const f = (value: string) => ({ dimension: 'country', operator: 'is' as const, values: [value] })
    const layout: DashboardLayout = { schema_version: 1, filters: [f('DE')], widgets: [] }
    const query = widgetQuery(layout, widget('a', 0, 0, { filters: [f('FR')] }), [f('US')], { base, timezone: 'UTC', now })
    expect(query.filters.map((item) => item.values[0])).toEqual(['DE', 'US', 'FR'])
  })
})
