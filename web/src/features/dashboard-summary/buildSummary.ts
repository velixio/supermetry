import { deltaValue } from '@/components/StatsStrip'
import type { Period } from '@/lib/search'

/** Totals the summary reads; the same figures the key-metric strip shows. */
export type SummaryTotals = { visitors: number; bounce_rate: number; visit_duration: number }
export type SummarySourceRow = { name: string; visitors: number }

export type SummaryInput = {
  period: Period
  range: { from: string; to: string }
  /** Any filter is active. */
  filtered: boolean
  reference: 'previous_period' | 'previous_year'
  totals: SummaryTotals
  previous?: SummaryTotals
  /** Top sources now and, when comparison is on, in the previous period. */
  sources?: { rows: SummarySourceRow[]; previousRows?: SummarySourceRow[] }
  topPage?: string
}

/** A typed sentence; the component words it through the message catalogue. */
export type Sentence =
  | { kind: 'few'; visitors: number }
  | { kind: 'headline'; visitors: number; period: Period; from: string; to: string; days: number; delta?: number; reference: SummaryInput['reference']; filtered: boolean }
  | { kind: 'mover'; source: string; change: number }
  | { kind: 'topPage'; page: string }
  | { kind: 'bounce'; from: number; to: number; better: boolean }
  | { kind: 'duration'; from: number; to: number; longer: boolean }

// Thresholds from the calm-analytics spec (C2).
export const FEW_VISITORS = 10
export const MOVER_MIN_VISITORS = 20
export const MOVER_MIN_SHARE = 0.3
export const BOUNCE_MIN_POINTS = 5
export const DURATION_MIN_RATIO = 0.2
export const MAX_SENTENCES = 3

function lengthDays(from: string, to: string): number {
  return Math.floor((Date.parse(`${to}T00:00:00Z`) - Date.parse(`${from}T00:00:00Z`)) / 86_400_000) + 1
}

function mover(input: SummaryInput): Sentence | undefined {
  const { sources, previous } = input
  if (!sources?.previousRows || !previous) return undefined
  const total = input.totals.visitors - previous.visitors
  if (total === 0) return undefined
  const before = new Map(sources.previousRows.map((row) => [row.name, row.visitors]))
  let best: { source: string; change: number } | undefined
  for (const row of sources.rows) {
    if (!row.name) continue
    const delta = row.visitors - (before.get(row.name) ?? 0)
    if (Math.sign(delta) !== Math.sign(total)) continue
    if (!best || Math.abs(delta) > Math.abs(best.change)) best = { source: row.name, change: delta }
  }
  if (!best || Math.abs(best.change) < MOVER_MIN_VISITORS || Math.abs(best.change) / Math.abs(total) < MOVER_MIN_SHARE) return undefined
  return { kind: 'mover', ...best }
}

function engagement(input: SummaryInput): Sentence | undefined {
  const { totals, previous } = input
  if (!previous) return undefined
  const points = totals.bounce_rate - previous.bounce_rate
  if (Math.abs(points) >= BOUNCE_MIN_POINTS) {
    return { kind: 'bounce', from: previous.bounce_rate, to: totals.bounce_rate, better: points < 0 }
  }
  const ratio = previous.visit_duration > 0 ? (totals.visit_duration - previous.visit_duration) / previous.visit_duration : 0
  if (Math.abs(ratio) >= DURATION_MIN_RATIO) {
    return { kind: 'duration', from: previous.visit_duration, to: totals.visit_duration, longer: ratio > 0 }
  }
  return undefined
}

/** Builds up to three typed sentences, in priority order, from figures the dashboard already has. */
export function buildSummary(input: SummaryInput): Sentence[] {
  if (input.totals.visitors < FEW_VISITORS) return [{ kind: 'few', visitors: input.totals.visitors }]
  const headline: Sentence = {
    kind: 'headline',
    visitors: input.totals.visitors,
    period: input.period,
    from: input.range.from,
    to: input.range.to,
    days: lengthDays(input.range.from, input.range.to),
    delta: input.previous ? deltaValue(input.totals.visitors, input.previous.visitors) : undefined,
    reference: input.reference,
    filtered: input.filtered,
  }
  const moved = mover(input)
  const shifted = engagement(input)
  const notable = [moved, shifted].filter((sentence): sentence is Sentence => sentence !== undefined)
  const page = input.topPage && (input.topPage !== '/' || notable.length === 0)
    ? ({ kind: 'topPage', page: input.topPage } satisfies Sentence)
    : undefined
  const ordered = [headline, moved, page, shifted].filter((sentence): sentence is Sentence => sentence !== undefined)
  return ordered.slice(0, MAX_SENTENCES)
}
