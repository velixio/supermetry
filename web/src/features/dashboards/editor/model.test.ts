import { describe, expect, it } from 'vitest'

import type { DashboardWidget } from '../api'
import {
  byteLength, isDirty, makeWidget, normalizeLayout, packWidgets, reduceDraft, templateLayout, validateDraft, limits,
  type Draft, type IssueText,
} from './model'

const w = (id: string, width: number, height = 4, extra: Partial<DashboardWidget> = {}): DashboardWidget => ({
  id, kind: 'goals', x: 0, y: 0, w: width, h: height, config: { limit: 5 }, ...extra,
})

const text: IssueText = {
  name: 'name', tooMany: (max) => `max ${max}`, tooLarge: 'large', tooTall: 'tall', title: (l) => `title ${l}`,
  filters: (max) => `filters ${max}`, metrics: (l) => `metrics ${l}`, dimension: (l) => `dimension ${l}`,
  funnel: (l) => `funnel ${l}`, limit: (l) => `limit ${l}`,
}
const label = (widget: DashboardWidget) => widget.id
const draft = (widgets: DashboardWidget[]): Draft => ({ name: 'Mine', layout: { schema_version: 1, widgets: packWidgets(widgets) } })

describe('packWidgets', () => {
  it('places widgets left to right and wraps when a row is full', () => {
    // 8 + 4 fill a row of 12 (height 5); 6 wraps to y=5; 6 sits beside it; 12 wraps to y=5+4.
    const packed = packWidgets([w('a', 8, 5), w('b', 4, 3), w('c', 6, 4), w('d', 6, 2), w('e', 12, 2)])
    expect(packed.map((item) => [item.id, item.x, item.y])).toEqual([
      ['a', 0, 0], ['b', 8, 0], ['c', 0, 5], ['d', 6, 5], ['e', 0, 9],
    ])
  })

  it('normalizeLayout sorts by reading order first', () => {
    const layout = normalizeLayout({
      schema_version: 1,
      widgets: [w('late', 6, 4, { x: 0, y: 8 }), w('right', 6, 4, { x: 6, y: 0 }), w('left', 6, 4, { x: 0, y: 0 })],
    })
    expect(layout.widgets.map((item) => item.id)).toEqual(['left', 'right', 'late'])
    expect(layout.widgets.map((item) => [item.x, item.y])).toEqual([[0, 0], [6, 0], [0, 4]])
  })
})

describe('reduceDraft', () => {
  const base = draft([w('a', 6), w('b', 6), w('c', 12)])

  it('moves a widget down and up and ignores moves past the ends', () => {
    const down = reduceDraft(base, { type: 'move', id: 'a', delta: 1 })
    expect(down.layout.widgets.map((item) => item.id)).toEqual(['b', 'a', 'c'])
    expect(reduceDraft(down, { type: 'move', id: 'a', delta: -1 }).layout).toEqual(base.layout)
    expect(reduceDraft(base, { type: 'move', id: 'a', delta: -1 })).toBe(base)
    expect(reduceDraft(base, { type: 'move', id: 'c', delta: 1 })).toBe(base)
  })

  it('resizes within the kind minimum and the 12 column grid and reflows', () => {
    const wide = reduceDraft(base, { type: 'resize', id: 'a', w: 99 })
    expect(wide.layout.widgets.map((item) => [item.id, item.w, item.x, item.y])).toEqual([['a', 12, 0, 0], ['b', 6, 0, 4], ['c', 12, 0, 8]])
    const narrow = reduceDraft(base, { type: 'resize', id: 'a', w: 1 })
    expect(narrow.layout.widgets[0].w).toBe(3)
  })

  it('removes, adds with a fresh id, renames and keeps ids on update', () => {
    const removed = reduceDraft(base, { type: 'remove', id: 'b' })
    expect(removed.layout.widgets.map((item) => item.id)).toEqual(['a', 'c'])
    const widget = makeWidget(base.layout, 'goals')
    expect(widget.id).toBe('goals-1')
    expect(makeWidget(reduceDraft(base, { type: 'add', widget }).layout, 'goals').id).toBe('goals-2')
    expect(reduceDraft(base, { type: 'rename', name: 'X' }).name).toBe('X')
    expect(reduceDraft(base, { type: 'update', id: 'a', patch: { id: 'zzz', title: 'T' } }).layout.widgets[0]).toMatchObject({ id: 'a', title: 'T' })
  })

  it('tracks dirtiness against the baseline and clears it when a change is undone', () => {
    expect(isDirty(base, base)).toBe(false)
    const changed = reduceDraft(base, { type: 'move', id: 'a', delta: 1 })
    expect(isDirty(changed, base)).toBe(true)
    expect(isDirty(reduceDraft(changed, { type: 'move', id: 'a', delta: -1 }), base)).toBe(false)
    expect(isDirty({ ...base, name: ' Mine ' }, base)).toBe(false)
  })
})

describe('validateDraft', () => {
  it('accepts every template', () => {
    for (const key of ['standard', 'marketing', 'product', 'performance', 'blank'] as const) {
      expect(validateDraft({ name: key, layout: templateLayout(key) }, text, label)).toEqual([])
    }
  })

  it('flags a blank name, 25 widgets and a funnel widget without a funnel', () => {
    const many = draft(Array.from({ length: limits.maxWidgets + 1 }, (_, i) => w(`g${i}`, 3, 1)))
    expect(validateDraft({ ...many, name: ' ' }, text, label).map((issue) => issue.message)).toEqual(expect.arrayContaining(['name', 'max 24']))
    const funnel = draft([makeWidget({ schema_version: 1, widgets: [] }, 'funnel')])
    expect(validateDraft(funnel, text, label)).toEqual([{ field: 'funnel-1', message: 'funnel funnel-1' }])
  })

  it('flags metric counts, limits, long titles, too many rows and oversize layouts', () => {
    const bad = draft([
      w('k', 12, 2, { kind: 'kpi_strip', config: { metrics: [] } }),
      w('b', 6, 5, { kind: 'breakdown', config: { dimension: 'page', metrics: ['visitors'], limit: 51 } }),
      w('t', 6, 5, { title: 'x'.repeat(81) }),
    ])
    expect(validateDraft(bad, text, label).map((issue) => issue.message)).toEqual(['metrics k', 'limit b', 'title t'])
    const tall = draft([w('a', 12, 12), ...Array.from({ length: 17 }, (_, i) => w(`r${i}`, 12, 12))])
    expect(validateDraft(tall, text, label).some((issue) => issue.message === 'tall')).toBe(true)
    const huge: Draft = { name: 'n', layout: { schema_version: 1, widgets: [w('a', 12, 4, { title: 'x', filters: [{ dimension: 'page', operator: 'is', values: ['y'.repeat(40_000)] }] })] } }
    expect(byteLength(huge.layout)).toBeGreaterThan(limits.maxBytes)
    expect(validateDraft(huge, text, label).some((issue) => issue.message === 'large')).toBe(true)
  })
})
