import { Fragment, useEffect, useId, useState, type ReactNode } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { ChevronDownIcon, ChevronRightIcon } from 'lucide-react'

import { statsStripQueries } from '@/components/StatsStrip'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { privateRequest } from '@/features/settings/privateApi'
import { fmt } from '@/lib/format'
import { formatNumber, interpolate } from '@/lib/i18n'
import type { Compare } from '@/lib/search'
import { useStats, type StatsQuery } from '@/lib/stats'
import { en } from '@/messages/en'

import { buildSummary, type Sentence, type SummaryInput, type SummarySourceRow, type SummaryTotals } from './buildSummary'

const emphasis = 'font-semibold text-foreground tabular-nums'

/** The period phrase of the headline ("in the last 7 days"), from the dashboard's period. */
function periodPhrase(sentence: Extract<Sentence, { kind: 'headline' }>): string {
  const t = en.dashboardSummary.period
  switch (sentence.period) {
    case 'today': return t.today
    case 'yesterday': return t.yesterday
    case 'month': return t.thisMonth
    case 'last_month': return t.lastMonth
    case 'year': return t.thisYear
    case '6mo': return interpolate(t.lastMonths, { months: 6 })
    case '12mo': return interpolate(t.lastMonths, { months: 12 })
    case 'custom': return interpolate(t.range, { from: fmt.date(sentence.from, 'UTC'), to: fmt.date(sentence.to, 'UTC') })
    default: return interpolate(t.lastDays, { days: sentence.days })
  }
}

/** Fills {placeholders}, wrapping the numeric ones in the emphasis style. */
function fill(template: string, values: Record<string, string>, strong: readonly string[] = []): ReactNode[] {
  return template.split(/(\{\w+\})/).map((part, index) => {
    const name = /^\{(\w+)\}$/.exec(part)?.[1]
    if (name === undefined || !(name in values)) return <Fragment key={index}>{part}</Fragment>
    return strong.includes(name) ? <span className={emphasis} key={index}>{values[name]}</span> : <Fragment key={index}>{values[name]}</Fragment>
  })
}

/** One sentence, worded through the catalogue. */
export function sentenceNodes(sentence: Sentence): ReactNode[] {
  const t = en.dashboardSummary
  switch (sentence.kind) {
    case 'few':
      return fill(t.few, { visitors: interpolate(t.visitorsCount, { count: sentence.visitors }) }, ['visitors'])
    case 'headline': {
      const values: Record<string, string> = {
        people: interpolate(t.people, { count: sentence.visitors }),
        period: periodPhrase(sentence),
        scope: sentence.filtered ? t.matchingFilters : '',
        reference: sentence.reference === 'previous_year'
          ? t.reference.previousYear
          : interpolate(t.reference.previousPeriod, { days: sentence.days }),
      }
      const { delta } = sentence
      if (delta === undefined) return fill(t.headlineOnly, values, ['people'])
      // Same rounding as the KPI cards, so the sentence and the card never disagree.
      values.percent = fmt.percent(Math.abs(delta))
      // A change that rounds to 0.0% reads as "about the same".
      if (Math.abs(delta) < 0.05) return fill(t.headlineSame, values, ['people'])
      return fill(delta > 0 ? t.headlineMore : t.headlineLess, values, ['people', 'percent'])
    }
    case 'mover':
      return fill(sentence.change > 0 ? t.moverUp : t.moverDown, { source: sentence.source, change: formatNumber(sentence.change, { signDisplay: 'always' }) }, ['change'])
    case 'topPage':
      return sentence.page === '/' ? [t.topPageHome] : fill(t.topPage, { page: sentence.page })
    case 'bounce':
      return fill(sentence.better ? t.bounceBetter : t.bounceWorse, { from: fmt.percent(sentence.from), to: fmt.percent(sentence.to) }, ['from', 'to'])
    case 'duration':
      return fill(sentence.longer ? t.durationLonger : t.durationShorter, { from: fmt.duration(sentence.from), to: fmt.duration(sentence.to) }, ['from', 'to'])
  }
}

type SummaryContext = Omit<SummaryInput, 'totals' | 'previous' | 'sources' | 'topPage' | 'reference'>

type Props = SummaryContext & {
  onFailed(failed: boolean): void
  query: StatsQuery
  compare: Compare
  /** Anonymous mode has no source or page breakdowns, so only the totals are used. */
  anonymous: boolean
  collapsed: boolean
}

function rowsOf(data: { rows?: Array<Record<string, unknown>> } | undefined, dimension: string): SummarySourceRow[] {
  return (data?.rows ?? []).map((row) => ({ name: String(row[dimension] ?? ''), visitors: Number(row.visitors ?? 0) }))
}

function totalsOf(data: { totals: Record<string, number> } | undefined): SummaryTotals | undefined {
  if (!data) return undefined
  return { visitors: data.totals.visitors ?? 0, bounce_rate: data.totals.bounce_rate ?? 0, visit_duration: data.totals.visit_duration ?? 0 }
}

type StatsResult = ReturnType<typeof useStats>
type Summary = { sentences?: Sentence[]; loading: boolean; failed: boolean }

/** The open summary. On an error the shell is hidden, because the key-metric strip already shows the error. */
function Lead({ summary, onFailed }: { summary: Summary; onFailed(failed: boolean): void }) {
  useEffect(() => onFailed(summary.failed), [summary.failed, onFailed])
  return summary.failed ? null : <LeadText summary={summary} />
}

function LeadText({ summary }: { summary: Summary }) {
  if (summary.loading || !summary.sentences) return <Skeleton aria-hidden="true" className="h-5 w-2/3" data-testid="dashboard-summary-loading" />
  return (
    <p className="max-w-3xl text-[0.9375rem] leading-6 text-foreground" data-testid="dashboard-summary-text">
      {summary.sentences.map((sentence, index) => <Fragment key={sentence.kind}>{index > 0 ? ' ' : null}{sentenceNodes(sentence)}</Fragment>)}
    </p>
  )
}

/** The queries of the key-metric strip: same keys, so these reads are served from its cache entries. */
function useStripTotals(props: Props): { reference: SummaryInput['reference']; current: StatsResult; comparison: StatsResult } {
  const { currentQuery, comparisonQuery, reference } = statsStripQueries(props.query, props.compare)
  return { reference, current: useStats(currentQuery), comparison: useStats(comparisonQuery) }
}

/** Anonymous mode has no source or page breakdowns, so only the totals are used. */
function TotalsLead(props: Props) {
  const { reference, current, comparison } = useStripTotals(props)
  const totals = totalsOf(current.data)
  return (
    <Lead onFailed={props.onFailed} summary={{
      loading: current.isLoading || comparison.isLoading,
      failed: current.isError,
      sentences: totals ? buildSummary({ ...props, reference, totals, previous: totalsOf(comparison.data) }) : undefined,
    }} />
  )
}

/** Also reads the first tabs of the Pages and Sources cards, again with the cards' own query shape. */
function FullLead(props: Props) {
  const { reference, current, comparison } = useStripTotals(props)
  const sources = useStats({ ...props.query, metrics: ['visitors'], dimensions: ['source'], orderBy: [{ column: 'visitors', direction: 'desc' }], limit: 10 })
  const pages = useStats({ ...props.query, metrics: ['visitors', 'pageviews', 'time_on_page', 'exit_rate'], dimensions: ['page'], orderBy: [{ column: 'visitors', direction: 'desc' }], limit: 10 })
  const totals = totalsOf(current.data)
  const loading = current.isLoading || comparison.isLoading || sources.isLoading || pages.isLoading
  // A failed breakdown only drops its sentence; the totals decide whether there is a summary at all.
  const comparisonRows = sources.data?.comparison?.rows
  return (
    <Lead onFailed={props.onFailed} summary={{
      loading,
      failed: current.isError,
      sentences: totals && !loading
        ? buildSummary({
          ...props,
          reference,
          totals,
          previous: totalsOf(comparison.data),
          sources: sources.data ? { rows: rowsOf(sources.data, 'source'), previousRows: comparisonRows ? rowsOf({ rows: comparisonRows }, 'source') : undefined } : undefined,
          topPage: pages.data ? rowsOf(pages.data, 'page')[0]?.name || undefined : undefined,
        })
        : undefined,
    }} />
  )
}

export type DashboardSummaryProps = Omit<Props, 'onFailed'>

/**
 * A plain-language lead above the key metrics. Collapsing it is remembered in the account preferences. It is
 * deliberately not a live region, so a refetch is not announced again.
 */
export function DashboardSummary(props: DashboardSummaryProps) {
  const regionId = useId()
  const queryClient = useQueryClient()
  const [failed, setFailed] = useState(false)
  const preference = useMutation({
    mutationFn: (summaryCollapsed: boolean) => privateRequest('PATCH', '/api/private/me', { body: { preferences: { summary_collapsed: summaryCollapsed } } }),
    onMutate: (summaryCollapsed) => {
      queryClient.setQueryData<Record<string, unknown>>(['me'], (current) => current ? {
        ...current,
        preferences: { landing_page: 'overview', sites_view: 'cards', ...(current.preferences as object | undefined), summary_collapsed: summaryCollapsed },
      } : current)
    },
  })
  const { collapsed } = props
  const Chevron = collapsed ? ChevronRightIcon : ChevronDownIcon
  const content = { ...props, onFailed: setFailed }
  return (
    <section aria-label={en.dashboardSummary.title} className="flex flex-col items-start gap-1" data-testid="dashboard-summary" hidden={failed && !collapsed}>
      <Button aria-controls={regionId} aria-expanded={!collapsed} className="-ml-2 h-7 px-2 text-xs font-medium text-muted-foreground" size="sm" type="button" variant="ghost" onClick={() => preference.mutate(!collapsed)}>
        {en.dashboardSummary.title}
        <Chevron data-icon="inline-end" />
      </Button>
      <div className="w-full" id={regionId}>
        {collapsed ? null : props.anonymous ? <TotalsLead {...content} /> : <FullLead {...content} />}
      </div>
    </section>
  )
}
