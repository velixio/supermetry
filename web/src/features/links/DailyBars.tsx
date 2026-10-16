import { fmt } from '@/lib/format'
import { en } from '@/messages/en'

import { bucketKey, type Granularity } from '../pixels/buckets'
import type { DayCount, HourCount } from './types'

const t = en.linksUi

/** Inclusive list of calendar days (YYYY-MM-DD) between two dates. */
export function daysBetween(from: string, to: string): string[] {
  const days: string[] = []
  const end = Date.parse(`${to}T00:00:00Z`)
  for (let time = Date.parse(`${from}T00:00:00Z`); time <= end && days.length < 400; time += 86_400_000) {
    days.push(new Date(time).toISOString().slice(0, 10))
  }
  return days
}

/** Chart buckets: the pixel granularities plus site-local hours. */
export type ChartGranularity = Granularity | 'hour'

type Props = {
  from: string
  to: string
  daily: DayCount[]
  hourly?: HourCount[]
  unit: string
  title: string
  granularity?: ChartGranularity
}

/** Every bucket in the range with its count; hour keys read YYYY-MM-DD HH:00. */
function chartBuckets(from: string, to: string, daily: DayCount[], hourly: HourCount[], granularity: ChartGranularity) {
  const buckets = new Map<string, number>()
  if (granularity === 'hour') {
    const byHour = new Map(hourly.map((item) => [item.hour, item.count]))
    for (const day of daysBetween(from, to)) {
      for (let hour = 0; hour < 24; hour++) {
        const clock = `${String(hour).padStart(2, '0')}:00`
        buckets.set(`${day} ${clock}`, byHour.get(`${day}T${clock}`) ?? 0)
      }
    }
  } else {
    const byDay = new Map(daily.map((item) => [item.day, item.count]))
    for (const day of daysBetween(from, to)) {
      const key = bucketKey(day, granularity)
      buckets.set(key, (buckets.get(key) ?? 0) + (byDay.get(day) ?? 0))
    }
  }
  return [...buckets].map(([day, count]) => ({ day, count }))
}

/** A bar-per-bucket chart with a text summary and an equivalent data table. */
export function DailyBars({ from, to, daily, hourly = [], unit, title, granularity = 'day' }: Props) {
  const days = chartBuckets(from, to, daily, hourly, granularity)
  const total = days.reduce((sum, item) => sum + item.count, 0)
  const peak = days.reduce((best, item) => (item.count > best.count ? item : best), days[0] ?? { day: from, count: 0 })
  if (total === 0) {
    return <p className="rounded-lg border border-dashed p-6 text-center text-sm text-muted-foreground">{t.dailyNone}</p>
  }
  const summary = (granularity === 'hour' ? t.hourlyAlt : t.dailyAlt)
    .replace('{total}', fmt.number(total))
    .replace('{from}', from)
    .replace('{to}', to)
    .replace('{peakDay}', peak.day)
    .replace('{peak}', fmt.number(peak.count))
  const width = Math.max(days.length, 1)
  return (
    <figure className="flex flex-col gap-2">
      <figcaption className="sr-only">{title}</figcaption>
      <div className="flex items-stretch gap-2">
        <span aria-hidden="true" className="flex w-10 shrink-0 flex-col justify-between text-xs text-muted-foreground tabular-nums">
          <span>{fmt.number(peak.count)}</span>
          <span>0</span>
        </span>
        <svg
          aria-label={summary}
          className="h-44 min-w-0 flex-1 rounded-md bg-muted/40"
          preserveAspectRatio="none"
          role="img"
          viewBox={`0 0 ${width} 100`}
        >
          {days.map((item, index) => {
            const height = (item.count / peak.count) * 96
            return (
              <rect
                className="fill-primary"
                height={Math.max(height, item.count > 0 ? 1 : 0)}
                key={item.day}
                width={0.78}
                x={index + 0.11}
                y={100 - height}
              >
                <title>{`${item.day}: ${fmt.number(item.count)} ${unit}`}</title>
              </rect>
            )
          })}
        </svg>
      </div>
      <div aria-hidden="true" className="ml-12 flex justify-between text-xs text-muted-foreground tabular-nums">
        <span>{granularity === 'hour' ? days[0].day : from}</span>
        <span>{granularity === 'hour' ? days[days.length - 1].day : to}</span>
      </div>
      <table className="sr-only">
        <caption>{title}</caption>
        <thead>
          <tr><th scope="col">{granularity === 'hour' ? t.hour : t.day}</th><th scope="col">{unit}</th></tr>
        </thead>
        <tbody>
          {days.filter((item) => item.count > 0).map((item) => (
            <tr key={item.day}><th scope="row">{item.day}</th><td>{item.count}</td></tr>
          ))}
        </tbody>
      </table>
    </figure>
  )
}
