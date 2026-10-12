import { fmt } from '@/lib/format'
import { en } from '@/messages/en'

import type { DayCount } from './types'

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

type Props = { from: string; to: string; daily: DayCount[]; unit: string; title: string }

/** A bar-per-day chart with a text summary and an equivalent data table. */
export function DailyBars({ from, to, daily, unit, title }: Props) {
  const byDay = new Map(daily.map((item) => [item.day, item.count]))
  const days = daysBetween(from, to).map((day) => ({ day, count: byDay.get(day) ?? 0 }))
  const total = days.reduce((sum, item) => sum + item.count, 0)
  const peak = days.reduce((best, item) => (item.count > best.count ? item : best), days[0] ?? { day: from, count: 0 })
  if (total === 0) {
    return <p className="rounded-lg border border-dashed p-6 text-center text-sm text-muted-foreground">{t.dailyNone}</p>
  }
  const summary = t.dailyAlt
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
        <span>{from}</span>
        <span>{to}</span>
      </div>
      <table className="sr-only">
        <caption>{title}</caption>
        <thead>
          <tr><th scope="col">{t.day}</th><th scope="col">{unit}</th></tr>
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
