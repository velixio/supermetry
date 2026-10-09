import { fmt } from '@/lib/format'

type ChartStep = { label: string; count: number }

export function FunnelChart({ steps, countLabel }: { steps: ChartStep[]; countLabel: 'Sessions' | 'Visitors' }) {
  const maximum = Math.max(0, ...steps.map((step) => step.count))
  const countDescription = countLabel.toLowerCase()

  return (
    <section aria-label="Funnel results" className="flex flex-col gap-3">
      <div className="flex flex-col gap-2" aria-hidden="true">
        {steps.map((step, index) => {
          const previous = steps[index - 1]?.count
          const conversion = previous && previous > 0 ? (step.count / previous) * 100 : 100
          const width = maximum > 0 ? Math.max(4, (step.count / maximum) * 100) : 4
          return (
            <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3" key={`${step.label}:${index}`}>
              <div className="relative min-w-0 overflow-hidden rounded-lg border bg-muted/30 px-3 py-2">
                <span className="absolute inset-y-0 left-0 bg-accent/15" style={{ width: `${width}%` }} />
                <span className="relative block truncate text-sm" title={step.label}>{step.label}</span>
              </div>
              <div className="min-w-24 text-right tabular-nums">
                <strong className="block text-sm">{fmt.number(step.count)}</strong>
                {index > 0 ? (
                  <span className="text-xs text-muted-foreground">
                    {fmt.percent(conversion)} · {fmt.percent(100 - conversion)} drop-off
                  </span>
                ) : null}
              </div>
            </div>
          )
        })}
      </div>
      <table className="sr-only">
        <caption>Funnel step {countDescription} and conversion from the previous step</caption>
        <thead><tr><th>Step</th><th>{countLabel}</th><th>Conversion</th><th>Drop-off</th></tr></thead>
        <tbody>
          {steps.map((step, index) => {
            const previous = steps[index - 1]?.count
            const conversion = previous && previous > 0 ? (step.count / previous) * 100 : 100
            return <tr key={`${step.label}:table:${index}`}><th>{step.label}</th><td>{step.count}</td><td>{fmt.percent(conversion)}</td><td>{index > 0 ? `${fmt.percent(100 - conversion)} drop-off` : '—'}</td></tr>
          })}
        </tbody>
      </table>
    </section>
  )
}
