import { fmt } from '@/lib/format'

import type { RetentionCohort } from './types'

function cellLabel(count: number, size: number): string {
  const percentage = size > 0 ? (count / size) * 100 : 0
  return `${count} ${count === 1 ? 'visitor' : 'visitors'}, ${fmt.percent(percentage)}`
}

export function RetentionHeatmap({
  cohorts,
  identityWindow,
  interval,
  retentionMode,
}: {
  cohorts: RetentionCohort[]
  identityWindow: string
  interval: 'day' | 'week' | 'month'
  retentionMode: string
}) {
  const columns = Math.max(0, ...cohorts.map((cohort) => cohort.retained.length))
  const usesProfiles = identityWindow === 'profile'
  const title = usesProfiles ? 'Profile retention' : 'Within-day returning sessions'

  return (
    <section className="flex min-w-0 flex-col gap-4">
      <div className="rounded-lg border bg-muted/30 px-4 py-3 text-sm">
        <p className="font-medium">{title}</p>
        <p className="text-muted-foreground">
          {usesProfiles
            ? `Identity window: ${identityWindow}. This view follows identified users returning across days. Mode: ${retentionMode}.`
            : `Identity window: ${identityWindow}. Supermetry cannot identify the same person across days; week and month views group daily cohorts without linking them. Mode: ${retentionMode}.`}
        </p>
      </div>
      <div className="min-w-0 overflow-x-auto rounded-xl border bg-card" tabIndex={0}>
        <table aria-label={title} className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b text-left">
              <th className="px-3 py-2 font-medium">Cohort</th>
              <th className="px-3 py-2 text-right font-medium">Size</th>
              {Array.from({ length: columns }, (_, index) => (
                <th className="min-w-28 px-3 py-2 text-center font-medium" key={index}>
                  {usesProfiles ? `${interval[0].toUpperCase()}${interval.slice(1)} ${index}` : index === 0 ? 'Session 1' : `Return ${index}`}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {cohorts.map((cohort) => (
              <tr className="border-b last:border-b-0" key={cohort.start}>
                <th className="whitespace-nowrap px-3 py-2 text-left font-medium">{cohort.start}</th>
                <td className="px-3 py-2 text-right tabular-nums">{fmt.number(cohort.size)}</td>
                {Array.from({ length: columns }, (_, index) => {
                  const count = cohort.retained[index]
                  if (count === undefined) return <td className="px-3 py-2 text-center text-muted-foreground" key={index}>—</td>
                  const percentage = cohort.size > 0 ? (count / cohort.size) * 100 : 0
                  return (
                    <td
                      aria-label={cellLabel(count, cohort.size)}
                      className="px-3 py-2 text-center tabular-nums"
                      key={index}
                      style={{ backgroundColor: `color-mix(in oklab, var(--primary) ${Math.min(60, Math.max(5, percentage * 0.6))}%, transparent)` }}
                    >
                      <span className="block font-medium">{fmt.percent(percentage)}</span>
                      <span className="block text-xs text-muted-foreground">{fmt.number(count)}</span>
                    </td>
                  )
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  )
}
