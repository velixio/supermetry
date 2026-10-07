import { fmt } from '@/lib/format'
import { useStats } from '@/lib/stats'

import { useRealtimePage } from './useRealtimePage'

type RealtimePageProps = {
  siteId: number
  dateRange: { from: string; to: string }
  timezone: string
}

type Point = { label: string; value: number }

function Sparkline({ points }: { points: Point[] }) {
  const maximum = Math.max(1, ...points.map((point) => point.value))
  const divisor = Math.max(1, points.length - 1)
  const polyline = points
    .map((point, index) => `${(index / divisor) * 100},${30 - (point.value / maximum) * 28}`)
    .join(' ')

  return (
    <div className="min-w-0">
      <svg aria-hidden="true" className="h-32 w-full text-primary" preserveAspectRatio="none" viewBox="0 0 100 32">
        <polyline fill="none" points={polyline} stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.5" vectorEffect="non-scaling-stroke" />
      </svg>
      <table className="sr-only">
        <caption>Recent pageview buckets</caption>
        <thead><tr><th>Time</th><th>Pageviews</th></tr></thead>
        <tbody>{points.map((point) => <tr key={point.label}><th>{point.label}</th><td>{point.value}</td></tr>)}</tbody>
      </table>
    </div>
  )
}

export function RealtimePage({ siteId, dateRange, timezone }: RealtimePageProps) {
  const realtime = useRealtimePage(siteId)
  const stats = useStats({
    siteId,
    dateRange,
    metrics: ['pageviews'],
    dimensions: ['time'],
    filters: [],
    timezone,
    live: true,
  })
  const points = (stats.data?.rows ?? []).slice(-30).map((row, index) => ({
    label: String(row.time ?? index + 1),
    value: Number(row.pageviews ?? 0),
  }))

  return (
    <main className="flex min-w-0 flex-col gap-6">
      <header>
        <h1 className="text-xl font-semibold tracking-tight">Realtime</h1>
        <p className="text-sm text-muted-foreground">Activity refreshes every 10 seconds.</p>
      </header>
      <section className="rounded-xl border bg-card p-5">
        <p className="text-sm text-muted-foreground">Visitors now</p>
        <p className="mt-1 text-4xl font-semibold tabular-nums">{fmt.number(realtime.data?.active_visitors ?? 0)}</p>
      </section>
      <section className="rounded-xl border bg-card p-5">
        <h2 className="mb-4 font-medium">Recent pageviews</h2>
        <Sparkline points={points} />
      </section>
      <section className="rounded-xl border bg-card p-5">
        <h2 className="mb-3 font-medium">Top pages now</h2>
        {realtime.data?.top_pages.length ? (
          <ol className="flex flex-col gap-2">
            {realtime.data.top_pages.map((page) => (
              <li className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] gap-3 text-sm" key={page.page}>
                <span className="truncate" title={page.page}>{page.page}</span>
                <span className="tabular-nums text-muted-foreground">{fmt.number(page.visitors)}</span>
              </li>
            ))}
          </ol>
        ) : <p className="text-sm text-muted-foreground">No active pages right now.</p>}
      </section>
    </main>
  )
}
