import { useMemo, useState } from 'react'

import worldPaths from '@/assets/world-paths.json'
import { countryLabel, normalizeCountryCode } from '@/lib/country'
import { fmt } from '@/lib/format'
import { cn } from '@/lib/utils'

import { Flag } from './Flag'

const WORLD_MAP_LABEL = 'World map'
const TABLE_LABEL = 'Country visitors'
const COUNTRY_HEADING = 'Country'
const VISITORS_HEADING = 'Visitors'
const VISITORS_SUFFIX = 'visitors'

const stepClasses = [
  'fill-muted',
  'fill-accent/20',
  'fill-accent/40',
  'fill-accent/60',
  'fill-accent/80',
  'fill-accent',
] as const

type WorldMapProps = {
  rows: { country: string; visitors: number }[]
  onSelect(code: string): void
}

type CountryRow = {
  country: string
  visitors: number
}

export function WorldMap({ rows, onSelect }: WorldMapProps) {
  const [activeCode, setActiveCode] = useState<string>()
  const data = useMemo(() => normalizeRows(rows), [rows])
  const visitorsByCountry = useMemo(
    () => new Map(data.map((row) => [row.country, row.visitors])),
    [data],
  )
  const maximum = data.reduce((largest, row) => Math.max(largest, row.visitors), 0)
  const activeRow = activeCode
    ? data.find((row) => row.country === activeCode)
    : undefined

  return (
    <div className="relative min-h-36 p-3">
      <svg
        aria-label={WORLD_MAP_LABEL}
        className="h-auto w-full"
        role="group"
        viewBox="0 0 1000 500"
      >
        {Object.entries(worldPaths).map(([country, path]) => {
          const visitors = visitorsByCountry.get(country) ?? 0
          const step = visitors > 0 && maximum > 0
            ? Math.max(1, Math.min(5, Math.ceil((visitors / maximum) * 5)))
            : 0
          const label = `${countryLabel(country)}: ${fmt.number(visitors)} ${VISITORS_SUFFIX}`
          const interactive = visitors > 0
          return (
            <path
              key={country}
              aria-label={interactive ? label : undefined}
              className={cn(
                'stroke-card stroke-[0.75] transition-colors outline-none',
                stepClasses[step],
                interactive ? 'cursor-pointer focus-visible:stroke-ring focus-visible:stroke-[2]' : null,
              )}
              d={path}
              data-country={country}
              data-step={step}
              role={interactive ? 'button' : undefined}
              tabIndex={interactive ? 0 : undefined}
              onBlur={() => setActiveCode(undefined)}
              onClick={interactive ? () => onSelect(country) : undefined}
              onFocus={() => setActiveCode(country)}
              onKeyDown={interactive ? (event) => {
                if (event.key === 'Enter' || event.key === ' ') {
                  event.preventDefault()
                  onSelect(country)
                }
              } : undefined}
              onMouseEnter={() => setActiveCode(country)}
              onMouseLeave={() => setActiveCode(undefined)}
            />
          )
        })}
      </svg>
      {activeRow ? (
        <div
          className="pointer-events-none absolute left-3 top-3 flex items-center gap-2 rounded-md bg-popover px-2.5 py-1.5 text-xs text-popover-foreground shadow-md ring-1 ring-border"
          role="tooltip"
        >
          <Flag code={activeRow.country} />
          <span>{countryLabel(activeRow.country)}</span>
          <span className="tabular-nums text-muted-foreground">{fmt.number(activeRow.visitors)}</span>
        </div>
      ) : null}
      <table aria-label={TABLE_LABEL} className="sr-only">
        <thead>
          <tr>
            <th scope="col">{COUNTRY_HEADING}</th>
            <th scope="col">{VISITORS_HEADING}</th>
          </tr>
        </thead>
        <tbody>
          {data.map((row) => (
            <tr key={row.country}>
              <th scope="row">{countryLabel(row.country)}</th>
              <td>{fmt.number(row.visitors)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function normalizeRows(rows: WorldMapProps['rows']): CountryRow[] {
  const totals = new Map<string, number>()
  for (const row of rows) {
    const country = normalizeCountryCode(row.country)
    if (!country || !Number.isFinite(row.visitors) || row.visitors <= 0) continue
    totals.set(country, (totals.get(country) ?? 0) + row.visitors)
  }
  return [...totals].map(([country, visitors]) => ({ country, visitors }))
}
