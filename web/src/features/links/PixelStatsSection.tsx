import { useMemo, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { fmt } from '@/lib/format'
import { resolveRange, type Period } from '@/lib/search'
import { en } from '@/messages/en'

import { usePixelStats } from './api'
import { type ChartGranularity, DailyBars, daysBetween } from './DailyBars'
import { PixelPlaces } from './PixelPlaces'

const t = en.linksUi

const periods: { value: Period; label: string }[] = [
  { value: 'today', label: t.pixelToday },
  { value: 'yesterday', label: t.periodYesterday },
  { value: '7d', label: t.last7 },
  { value: '30d', label: t.last30 },
  { value: '91d', label: t.last90 },
  { value: '6mo', label: t.last6mo },
  { value: '12mo', label: t.last12mo },
]

type Props = {
  siteId: number
  timezone: string
  tag: string
}

/** Longest inclusive range the API returns hourly counts for. */
const MAX_HOURLY_DAYS = 7

/** Period cards and grouped impression chart for one managed tracking pixel. */
export function PixelStatsSection({ siteId, timezone, tag }: Props) {
  const [period, setPeriod] = useState<Period>('30d')
  const [granularity, setGranularity] = useState<ChartGranularity>('day')
  const range = useMemo(() => resolveRange(period, timezone, new Date()), [period, timezone])
  const stats = usePixelStats(siteId, range, tag)
  const hourAllowed = daysBetween(range.from, range.to).length <= MAX_HOURLY_DAYS
  const choosePeriod = (next: Period) => {
    const nextRange = resolveRange(next, timezone, new Date())
    const days = daysBetween(nextRange.from, nextRange.to).length
    setPeriod(next)
    // A day or two reads best by hour; hours are only stored for short ranges.
    if (days <= 2) setGranularity('hour')
    else if (granularity === 'hour' && days > MAX_HOURLY_DAYS) setGranularity('day')
  }
  const chooseGranularity = (next: ChartGranularity) => {
    setGranularity(next)
    // Hours are only served for short ranges, so narrow a longer period.
    if (next === 'hour' && !hourAllowed) setPeriod('7d')
  }
  return (
    <section aria-labelledby="pixel-stats" className="flex flex-col gap-4 rounded-xl border bg-card p-4">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <h2 className="text-base font-medium" id="pixel-stats">{t.pixelStatsTitle}</h2>
        <div className="flex flex-wrap gap-3">
          <div className="flex flex-col gap-1">
            <Label htmlFor="pixel-group">{t.groupBy}</Label>
            <select
              id="pixel-group"
              className="min-h-11 rounded-lg border border-input bg-background px-3 text-sm"
              value={granularity}
              onChange={(event) => chooseGranularity(event.target.value as ChartGranularity)}
            >
              <option value="hour">{t.byHour}</option>
              <option value="day">{t.byDay}</option>
              <option value="week">{t.byWeek}</option>
              <option value="month">{t.byMonth}</option>
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <Label htmlFor="pixel-period">{t.period}</Label>
            <select
              id="pixel-period"
              className="min-h-11 rounded-lg border border-input bg-background px-3 text-sm"
              value={period}
              onChange={(event) => choosePeriod(event.target.value as Period)}
            >
              {periods.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
            </select>
          </div>
        </div>
      </div>
      {stats.isPending ? <p className="text-sm text-muted-foreground" role="status">{t.loading}</p> : null}
      {stats.isError ? (
        <div className="flex items-center gap-3 text-sm" role="alert">
          {stats.error.message}
          <Button className="min-h-11" variant="outline" onClick={() => void stats.refetch()}>{t.retry}</Button>
        </div>
      ) : null}
      {stats.data ? (
        <>
          <dl className="grid grid-cols-2 gap-3 md:grid-cols-4" aria-label={t.pixelPeriodCards}>
            {([[t.pixelToday, stats.data.summary.today], [t.last7, stats.data.summary.last_7], [t.last30, stats.data.summary.last_30], [t.pixelAllTime, stats.data.summary.total]] as const).map(([label, value]) => (
              <div className="rounded-lg border p-3" key={label}>
                <dt className="text-xs text-muted-foreground">{label}</dt>
                <dd className="text-2xl font-semibold tabular-nums">{fmt.number(value)}</dd>
              </div>
            ))}
          </dl>
          <p className="text-xs text-muted-foreground">{t.pixelImpressions}: {fmt.number(stats.data.total)}</p>
          <DailyBars
            daily={stats.data.daily}
            from={stats.data.from}
            granularity={granularity === 'hour' && !hourAllowed ? 'day' : granularity}
            hourly={stats.data.hourly}
            title={t.pixelStatsTitle}
            to={stats.data.to}
            unit={t.pixelStatsTitle}
          />
          <PixelPlaces stats={stats.data} />
        </>
      ) : null}
    </section>
  )
}
