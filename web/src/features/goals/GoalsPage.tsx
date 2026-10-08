import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { fmt } from '@/lib/format'
import { useStats } from '@/lib/stats'

import { useGoals, useSaveGoal } from './api'
import { GoalEditor } from './GoalEditor'
import type { Goal } from './types'

type GoalsPageProps = { siteId: number; dateRange: { from: string; to: string } }

export function GoalsPage({ siteId, dateRange }: GoalsPageProps) {
  const goals = useGoals(siteId)
  const save = useSaveGoal(siteId)
  const [editing, setEditing] = useState<Goal>()

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
              {(goals.data ?? []).map((goal) => <GoalRow dateRange={dateRange} goal={goal} key={goal.id} siteId={siteId} onEdit={() => setEditing(goal)} />)}
            </tbody>
          </table>
          {goals.data?.length === 0 ? <p className="p-5 text-sm text-muted-foreground">No goals yet.</p> : null}
        </section>
        <GoalEditor
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

function GoalRow({ goal, siteId, dateRange, onEdit }: { goal: Goal; siteId: number; dateRange: GoalsPageProps['dateRange']; onEdit: () => void }) {
  const matched = useStats({
    siteId,
    dateRange,
    metrics: ['visitors'],
    filters: [{ dimension: 'goal_id', operator: 'is', values: [String(goal.id)] }],
    timezone: 'UTC',
  })
  const all = useStats({ siteId, dateRange, metrics: ['visitors'], filters: [], timezone: 'UTC' })
  const conversions = matched.data?.totals?.visitors ?? 0
  const visitors = all.data?.totals?.visitors ?? 0
  const rate = visitors > 0 ? (conversions / visitors) * 100 : 0
  const description = goal.kind === 'scroll' ? `Scroll ≥ ${goal.scroll_threshold}% on ${goal.value}` : null
  return <tr className="border-b last:border-0"><th className="max-w-56 px-4 py-3 text-left font-medium" title={goal.name}><span className="block truncate">{goal.name}</span>{description ? <span className="block truncate text-xs font-normal text-muted-foreground">{description}</span> : null}</th><td className="px-4 py-3 text-right tabular-nums">{fmt.number(conversions)}</td><td className="px-4 py-3 text-right tabular-nums">{fmt.percent(rate)}</td>{goal.currency ? <RevenueCells dateRange={dateRange} goal={goal} siteId={siteId} /> : <><td className="px-4 py-3 text-right text-muted-foreground">—</td><td className="px-4 py-3 text-right text-muted-foreground">—</td></>}<td className="px-4 py-2 text-right"><Button size="sm" type="button" variant="ghost" onClick={onEdit}>Edit</Button></td></tr>
}

function RevenueCells({ goal, siteId, dateRange }: { goal: Goal; siteId: number; dateRange: GoalsPageProps['dateRange'] }) {
  const currency = goal.currency ?? 'USD'
  const revenue = useStats({
    siteId,
    dateRange,
    metrics: ['total_revenue', 'average_revenue'],
    filters: [
      { dimension: 'goal_id', operator: 'is', values: [String(goal.id)] },
      { dimension: 'revenue_currency', operator: 'is', values: [currency] },
    ],
    timezone: 'UTC',
  })
  const formatter = new Intl.NumberFormat('en-US', { style: 'currency', currency })
  return <><td className="px-4 py-3 text-right tabular-nums">{formatter.format(revenue.data?.totals?.total_revenue ?? 0)}</td><td className="px-4 py-3 text-right tabular-nums">{formatter.format(revenue.data?.totals?.average_revenue ?? 0)}</td></>
}
