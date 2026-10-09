import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { fmt } from '@/lib/format'
import { useStats, type StatsQuery } from '@/lib/stats'

import { useGoals, useSaveGoal } from './api'
import { GoalEditor } from './GoalEditor'
import type { Goal } from './types'

type GoalsPageProps = {
  siteId: number
  dateRange: { from: string; to: string }
  timezone?: string
  revenueCurrency?: string
  revenueCurrencyState?: 'unconfigured' | 'configured' | 'conflict'
}

export function GoalsPage({ siteId, dateRange, timezone = 'UTC', revenueCurrency, revenueCurrencyState }: GoalsPageProps) {
  const goals = useGoals(siteId)
  const save = useSaveGoal(siteId)
  const [editing, setEditing] = useState<Goal>()
  const conversions = useStats({
    siteId,
    dateRange,
    metrics: ['conversions', 'visits'] as StatsQuery['metrics'],
    dimensions: ['goal'],
    filters: [],
    timezone,
  })
  const conversionByGoal = new Map(
    (conversions.data?.rows ?? []).map((row) => [String(row.goal ?? ''), Number(row.conversions)]),
  )
  const visits = conversions.data?.totals?.visits

  return (
    <main className="flex min-w-0 flex-col gap-6">
      <header>
        <h1 className="text-xl font-semibold tracking-tight">Goals</h1>
        <p className="text-sm text-muted-foreground">Track the pages and events that matter.</p>
      </header>
      <div className="grid min-w-0 gap-6 xl:grid-cols-[minmax(0,1.5fr)_minmax(18rem,0.8fr)]">
        <section className="overflow-hidden rounded-xl border bg-card">
          <table className="w-full text-sm">
            <thead className="border-b text-left text-muted-foreground"><tr><th className="px-4 py-3 font-medium">Goal</th><th className="px-4 py-3 text-right font-medium">Conversions</th><th className="px-4 py-3 text-right font-medium">Rate</th><th className="px-4 py-3 text-right font-medium">Revenue</th><th className="px-4 py-3 text-right font-medium">Average revenue</th><th><span className="sr-only">Actions</span></th></tr></thead>
            <tbody>
              {(goals.data ?? []).map((goal) => <GoalRow
                conversionsQuery={conversions}
                conversions={conversions.data ? (conversionByGoal.get(String(goal.id)) ?? 0) : undefined}
                dateRange={dateRange}
                goal={goal}
                key={goal.id}
                siteId={siteId}
                timezone={timezone}
                visits={visits}
                onEdit={() => setEditing(goal)}
              />)}
            </tbody>
          </table>
          {goals.isPending ? <p className="p-5 text-sm text-muted-foreground" role="status">Loading goals…</p> : null}
          {goals.isError ? <div className="p-5" role="alert">Goals could not be loaded. <Button variant="outline" size="sm" onClick={() => void goals.refetch()}>Retry goals</Button></div> : null}
          {!goals.isPending && !goals.isError && goals.data?.length === 0 ? <p className="p-5 text-sm text-muted-foreground">No goals yet.</p> : null}
        </section>
        <GoalEditor
          projectCurrency={revenueCurrency}
          currencyState={revenueCurrencyState}
          goal={editing}
          pending={save.isPending}
          onCancel={() => setEditing(undefined)}
          onSave={async (input) => {
            await save.mutateAsync({ id: editing?.id, input })
            setEditing(undefined)
          }}
        />
      </div>
    </main>
  )
}

function GoalRow({ goal, siteId, dateRange, timezone, conversionsQuery, conversions, visits, onEdit }: {
  goal: Goal
  siteId: number
  dateRange: GoalsPageProps['dateRange']
  timezone: string
  conversionsQuery: ReturnType<typeof useStats>
  conversions: number | undefined
  visits: number | undefined
  onEdit: () => void
}) {
  const rate = conversions === undefined || visits === undefined || !Number.isFinite(conversions) || !Number.isFinite(visits)
    ? undefined : visits > 0 ? (conversions / visits) * 100 : 0
  const description = goal.kind === 'scroll' ? `Scroll ≥ ${goal.scroll_threshold}% on ${goal.value}` : null
  return (
    <tr className="border-b last:border-0">
      <th className="max-w-56 px-4 py-3 text-left font-medium" title={goal.name}>
        <span className="block truncate">{goal.name}</span>
        {description ? <span className="block truncate text-xs font-normal text-muted-foreground">{description}</span> : null}
      </th>
      <MetricCell label="conversions" queries={[conversionsQuery]} value={conversions} format={fmt.number} />
      <MetricCell label="conversion rate" queries={[conversionsQuery]} value={rate} format={fmt.percent} />
      {goal.currency ? goal.kind === 'event'
        ? <RevenueCells currency={goal.currency} dateRange={dateRange} goal={goal} siteId={siteId} timezone={timezone} />
        : <td colSpan={2} className="px-4 py-3 text-right text-muted-foreground">Revenue unavailable for this goal</td>
        : <><td className="px-4 py-3 text-right text-muted-foreground">—</td><td className="px-4 py-3 text-right text-muted-foreground">—</td></>}
      <td className="px-4 py-2 text-right"><Button size="sm" type="button" variant="ghost" onClick={onEdit}>Edit</Button></td>
    </tr>
  )
}

function RevenueCells({ goal, siteId, dateRange, currency, timezone }: { goal: Goal; siteId: number; dateRange: GoalsPageProps['dateRange']; currency: string; timezone: string }) {
  const revenue = useStats({
    siteId,
    dateRange,
    metrics: ['total_revenue', 'average_revenue'],
    filters: [
      { dimension: 'goal_id', operator: 'is', values: [String(goal.id)] },
      { dimension: 'revenue_currency', operator: 'is', values: [currency] },
    ],
    timezone,
  })
  const formatter = new Intl.NumberFormat('en-US', { style: 'currency', currency })
  if (revenue.isPending || revenue.isFetching || revenue.isError || !Number.isFinite(revenue.data?.totals?.total_revenue) || !Number.isFinite(revenue.data?.totals?.average_revenue)) {
    return <MetricCell label="revenue" queries={[revenue]} value={undefined} format={formatter.format} colSpan={2} />
  }
  return <>
    <MetricCell label="revenue" queries={[revenue]} value={revenue.data?.totals?.total_revenue} format={formatter.format} />
    <MetricCell label="average revenue" queries={[revenue]} value={revenue.data?.totals?.average_revenue} format={formatter.format} />
  </>
}

function MetricCell({ label, queries, value, format, colSpan }: {
  colSpan?: number
  label: string
  queries: ReturnType<typeof useStats>[]
  value: number | undefined
  format: (value: number) => string
}) {
  const title = label.charAt(0).toUpperCase() + label.slice(1)
  const pending = queries.some((query) => query.isPending || query.isFetching)
  const failed = queries.some((query) => query.isError)
  const missing = value === undefined || !Number.isFinite(value)
  return <td colSpan={colSpan} className="px-4 py-3 text-right tabular-nums">
    {pending ? <span className="text-muted-foreground" role="status">Loading {label}…</span>
      : failed || missing ? <span className="text-muted-foreground">
        <span role={failed ? 'alert' : undefined}>{failed ? `Could not load ${label}` : `${title} unavailable`}</span>
        <Button className="ml-2" size="sm" type="button" variant="outline" aria-label={`Retry ${label}`} onClick={() => { for (const query of queries) void query.refetch() }}>Retry</Button>
      </span>
      : format(value)}
  </td>
}
