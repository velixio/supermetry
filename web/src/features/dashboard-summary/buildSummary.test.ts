import { describe, expect, it } from 'vitest'

import { buildSummary, type SummaryInput } from './buildSummary'

const base: SummaryInput = {
  period: '7d',
  range: { from: '2026-11-12', to: '2026-11-18' },
  filtered: false,
  reference: 'previous_period',
  totals: { visitors: 1240, bounce_rate: 50, visit_duration: 100 },
  previous: { visitors: 1050, bounce_rate: 50, visit_duration: 100 },
}

describe('buildSummary', () => {
  it('always leads with a headline carrying the hand-computed change', () => {
    // (1240 - 1050) / 1050 = 18.095...%
    const [headline, ...rest] = buildSummary(base)
    expect(headline).toMatchObject({ kind: 'headline', visitors: 1240, period: '7d', days: 7, reference: 'previous_period', filtered: false })
    expect((headline as { delta: number }).delta).toBeCloseTo(18.095, 2)
    expect(rest).toEqual([])
  })

  it('keeps the delta when comparison is off, because the strip always computes it', () => {
    const [headline] = buildSummary({ ...base, reference: 'previous_period' })
    expect((headline as { delta?: number }).delta).toBeDefined()
  })

  it('has no delta without previous data or with a zero previous period', () => {
    expect((buildSummary({ ...base, previous: undefined })[0] as { delta?: number }).delta).toBeUndefined()
    expect((buildSummary({ ...base, previous: { ...base.previous!, visitors: 0 } })[0] as { delta?: number }).delta).toBeUndefined()
  })

  it('marks filtered headlines', () => {
    expect(buildSummary({ ...base, filtered: true })[0]).toMatchObject({ filtered: true })
  })

  it('says there is not much traffic below 10 visitors and nothing else', () => {
    expect(buildSummary({ ...base, totals: { ...base.totals, visitors: 9 }, sources: { rows: [{ name: 'google.com', visitors: 9 }], previousRows: [] } }))
      .toEqual([{ kind: 'few', visitors: 9 }])
    expect(buildSummary({ ...base, totals: { ...base.totals, visitors: 10 } })[0]?.kind).toBe('headline')
  })

  describe('biggest mover among sources', () => {
    const sources = (rows: Array<[string, number]>, previous: Array<[string, number]>) => ({
      rows: rows.map(([name, visitors]) => ({ name, visitors })),
      previousRows: previous.map(([name, visitors]) => ({ name, visitors })),
    })
    // total change +190

    it('names the source with the largest change when it is at least 20 visitors and 30% of the total change', () => {
      // google.com +100 (52.6% of 190), bing.com +30
      const out = buildSummary({ ...base, sources: sources([['google.com', 400], ['bing.com', 130]], [['google.com', 300], ['bing.com', 100]]) })
      expect(out[1]).toEqual({ kind: 'mover', source: 'google.com', change: 100 })
    })

    const withTotal = (visitors: number) => ({ ...base, totals: { ...base.totals, visitors }, previous: { ...base.previous!, visitors: 1050 } })

    it('is shown at exactly 30% of the total change', () => {
      const out = buildSummary({ ...withTotal(1150), sources: sources([['a.com', 130]], [['a.com', 100]]) })
      expect(out[1]).toEqual({ kind: 'mover', source: 'a.com', change: 30 })
    })

    it('is hidden just under 30% of the total change', () => {
      const out = buildSummary({ ...withTotal(1150), sources: sources([['a.com', 129]], [['a.com', 100]]) })
      expect(out.map((s) => s.kind)).toEqual(['headline'])
    })

    it('is shown at exactly 20 visitors', () => {
      const out = buildSummary({ ...withTotal(1090), sources: sources([['a.com', 120]], [['a.com', 100]]) })
      expect(out[1]).toEqual({ kind: 'mover', source: 'a.com', change: 20 })
    })

    it('is hidden at 19 visitors even when it is most of the change', () => {
      const out = buildSummary({ ...withTotal(1090), sources: sources([['a.com', 119]], [['a.com', 100]]) })
      expect(out.map((s) => s.kind)).toEqual(['headline'])
    })

    it('is hidden below 30% of the total change', () => {
      // total +190, source +50 = 26%
      const out = buildSummary({ ...base, sources: sources([['a.com', 150]], [['a.com', 100]]) })
      expect(out.map((s) => s.kind)).toEqual(['headline'])
    })

    it('reports a drop when the total fell and the biggest mover fell with it', () => {
      const out = buildSummary({
        ...base,
        totals: { ...base.totals, visitors: 1000 }, // change -50
        sources: sources([['a.com', 100]], [['a.com', 150]]),
      })
      expect(out[1]).toEqual({ kind: 'mover', source: 'a.com', change: -50 })
    })

    it('ignores a mover that moved against the total, and sources with no name', () => {
      const out = buildSummary({ ...base, sources: sources([['a.com', 100], ['', 900]], [['a.com', 200], ['', 100]]) })
      expect(out.map((s) => s.kind)).toEqual(['headline'])
    })

    it('treats a new source as starting from zero', () => {
      const out = buildSummary({ ...base, sources: sources([['new.com', 120]], []) })
      expect(out[1]).toEqual({ kind: 'mover', source: 'new.com', change: 120 })
    })

    it('is skipped without previous source rows (comparison off)', () => {
      const out = buildSummary({ ...base, sources: { rows: [{ name: 'a.com', visitors: 900 }] } })
      expect(out.map((s) => s.kind)).toEqual(['headline'])
    })
  })

  describe('top page', () => {
    it('is shown when it is not the home page', () => {
      expect(buildSummary({ ...base, topPage: '/pricing' })[1]).toEqual({ kind: 'topPage', page: '/pricing' })
    })

    it('is hidden for / when other facts exist', () => {
      const out = buildSummary({ ...base, topPage: '/', previous: { ...base.previous!, bounce_rate: 60 } })
      expect(out.map((s) => s.kind)).toEqual(['headline', 'bounce'])
    })

    it('is shown for / when it is the only notable fact', () => {
      expect(buildSummary({ ...base, topPage: '/' })[1]).toEqual({ kind: 'topPage', page: '/' })
    })
  })

  describe('engagement shift', () => {
    it('reports an improved bounce rate at exactly 5 points', () => {
      const out = buildSummary({ ...base, totals: { ...base.totals, bounce_rate: 45 }, previous: { ...base.previous!, bounce_rate: 50 } })
      expect(out[1]).toEqual({ kind: 'bounce', from: 50, to: 45, better: true })
    })

    it('stays quiet at 4.9 points', () => {
      const out = buildSummary({ ...base, totals: { ...base.totals, bounce_rate: 45.1 }, previous: { ...base.previous!, bounce_rate: 50 } })
      expect(out.map((s) => s.kind)).toEqual(['headline'])
    })

    it('reports a worse bounce rate', () => {
      const out = buildSummary({ ...base, totals: { ...base.totals, bounce_rate: 58 } })
      expect(out[1]).toEqual({ kind: 'bounce', from: 50, to: 58, better: false })
    })

    it('reports visit duration at exactly 20%, longer or shorter', () => {
      expect(buildSummary({ ...base, totals: { ...base.totals, visit_duration: 120 } })[1]).toEqual({ kind: 'duration', from: 100, to: 120, longer: true })
      expect(buildSummary({ ...base, totals: { ...base.totals, visit_duration: 80 } })[1]).toEqual({ kind: 'duration', from: 100, to: 80, longer: false })
      expect(buildSummary({ ...base, totals: { ...base.totals, visit_duration: 119 } }).map((s) => s.kind)).toEqual(['headline'])
    })

    it('ignores duration when there was none before', () => {
      expect(buildSummary({ ...base, previous: { ...base.previous!, visit_duration: 0 } }).map((s) => s.kind)).toEqual(['headline'])
    })
  })

  it('shows at most three sentences in priority order', () => {
    const out = buildSummary({
      ...base,
      topPage: '/pricing',
      totals: { ...base.totals, bounce_rate: 40, visit_duration: 150 },
      sources: { rows: [{ name: 'google.com', visitors: 400 }], previousRows: [{ name: 'google.com', visitors: 300 }] },
    })
    expect(out.map((s) => s.kind)).toEqual(['headline', 'mover', 'topPage'])
  })
})
