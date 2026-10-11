import { useMemo, useState } from 'react'

import worldPaths from '@/assets/world-paths.json'
import { rampStep } from '@/features/map/rampStep'
import { countryLabel, normalizeCountryCode } from '@/lib/country'
import { fmt } from '@/lib/format'
import { cn } from '@/lib/utils'
import { en } from '@/messages/en'

import { Flag } from './Flag'

const WORLD_MAP_LABEL = 'World map'
const TABLE_LABEL = 'Country visitors'
const COUNTRY_HEADING = 'Country'
const VISITORS_HEADING = 'Visitors'
const VISITORS_SUFFIX = 'visitors'
const stepClasses = ['fill-muted', 'fill-accent/20', 'fill-accent/40', 'fill-accent/60', 'fill-accent/80', 'fill-accent'] as const

type WorldMapProps = {
  rows: { country: string; visitors: number; pageviews?: number }[]
  metric?: 'visitors' | 'pageviews'
  selectedCountry?: string
  showLegend?: boolean
  onClear?(): void
  onSelect(code: string): void
}
type CountryRow = { country: string; visitors: number; pageviews: number }

const fullNumber = new Intl.NumberFormat('en-US')
const percentage = new Intl.NumberFormat('en-US', { maximumFractionDigits: 1 })

export function WorldMap({ rows, metric = 'visitors', selectedCountry, showLegend = false, onClear, onSelect }: WorldMapProps) {
  const [activeCode, setActiveCode] = useState<string>()
  const data = useMemo(() => normalizeRows(rows), [rows])
  const valueByCountry = useMemo(() => new Map(data.map((row) => [row.country, row[metric]])), [data, metric])
  const maximum = data.reduce((largest, row) => Math.max(largest, row[metric]), 0)
  const total = data.reduce((sum, row) => sum + row[metric], 0)
  const paths = useMemo(() => Object.entries(worldPaths).sort(([left], [right]) => {
    if (left === selectedCountry) return 1
    if (right === selectedCountry) return -1
    const difference = (valueByCountry.get(right) ?? 0) - (valueByCountry.get(left) ?? 0)
    return difference || left.localeCompare(right)
  }), [selectedCountry, valueByCountry])
  const activeRow = activeCode ? data.find((row) => row.country === activeCode) : undefined
  const metricLabel = metric === 'visitors' ? en.map.visitors : en.map.pageviews
  const metricSuffix = metricLabel.toLowerCase()

  return (
    <div className="relative flex h-full min-h-36 flex-col p-3">
      <svg aria-label={WORLD_MAP_LABEL} className="min-h-0 w-full flex-1" role="group" viewBox="0 0 1000 500">
        {paths.map(([country, path]) => {
          const value = valueByCountry.get(country) ?? 0
          const step = rampStep(value, maximum)
          const share = total > 0 ? (value / total) * 100 : 0
          const label = showLegend
            ? `${countryLabel(country)}: ${fullNumber.format(value)} ${metricSuffix}, ${percentage.format(share)} ${en.map.percent}`
            : `${countryLabel(country)}: ${fmt.number(value)} ${VISITORS_SUFFIX}`
          const interactive = value > 0
          const selected = country === selectedCountry
          return <path
            key={country}
            aria-label={interactive ? label : undefined}
            aria-pressed={interactive ? selected : undefined}
            className={cn(
              'stroke-background stroke-[1] transition-colors outline-none',
              stepClasses[step],
              interactive ? 'cursor-pointer hover:stroke-ring hover:stroke-[2] focus-visible:stroke-ring focus-visible:stroke-[2]' : null,
              selected ? 'stroke-ring stroke-[2]' : null,
            )}
            d={path}
            data-country={country}
            data-step={step}
            role={interactive ? 'button' : undefined}
            tabIndex={interactive ? 0 : undefined}
            vectorEffect="non-scaling-stroke"
            onBlur={() => setActiveCode(undefined)}
            onClick={interactive ? () => onSelect(country) : undefined}
            onFocus={() => setActiveCode(country)}
            onKeyDown={interactive ? (event) => {
              if (event.key === 'Enter' || event.key === ' ') {
                event.preventDefault()
                onSelect(country)
              } else if (event.key === 'Escape') {
                event.preventDefault()
                onClear?.()
              }
            } : undefined}
            onMouseEnter={() => setActiveCode(country)}
            onMouseLeave={() => setActiveCode(undefined)}
          />
        })}
      </svg>
      {activeRow ? <div className="pointer-events-none absolute left-3 top-3 flex items-center gap-2 rounded-md bg-popover px-2.5 py-1.5 text-xs text-popover-foreground shadow-md ring-1 ring-border" role="tooltip">
        <Flag code={activeRow.country} />
        <span>{countryLabel(activeRow.country)}</span>
        <span className="tabular-nums text-muted-foreground">
          {showLegend ? `${fullNumber.format(activeRow[metric])} ${metricSuffix}` : fmt.number(activeRow[metric])}
          {showLegend && total > 0 ? ` · ${percentage.format((activeRow[metric] / total) * 100)}%` : ''}
        </span>
      </div> : null}
      {showLegend ? <div aria-label={en.map.legend} className="flex items-center gap-2 px-1 pt-3 text-xs text-muted-foreground">
        <span className="tabular-nums">0</span>
        <span aria-hidden="true" className="flex flex-1 overflow-hidden rounded-sm">
          {stepClasses.slice(1).map((stepClass) => <span key={stepClass} className={cn('h-2 flex-1', stepClass.replace('fill-', 'bg-'))} />)}
        </span>
        <span>{metricLabel}</span>
        <span className="tabular-nums">{fullNumber.format(maximum)}</span>
      </div> : null}
      <table aria-label={TABLE_LABEL} className="sr-only">
        <thead><tr><th scope="col">{COUNTRY_HEADING}</th><th scope="col">{showLegend ? metricLabel : VISITORS_HEADING}</th></tr></thead>
        <tbody>{data.map((row) => <tr key={row.country}><th scope="row">{countryLabel(row.country)}</th><td>{fmt.number(row[metric])}</td></tr>)}</tbody>
      </table>
    </div>
  )
}

function normalizeRows(rows: WorldMapProps['rows']): CountryRow[] {
  const totals = new Map<string, Omit<CountryRow, 'country'>>()
  for (const row of rows) {
    const country = normalizeCountryCode(row.country)
    if (!country) continue
    const visitors = Number.isFinite(row.visitors) && row.visitors > 0 ? row.visitors : 0
    const pageviews = Number.isFinite(row.pageviews) && Number(row.pageviews) > 0 ? Number(row.pageviews) : 0
    if (visitors === 0 && pageviews === 0) continue
    const current = totals.get(country) ?? { visitors: 0, pageviews: 0 }
    totals.set(country, { visitors: current.visitors + visitors, pageviews: current.pageviews + pageviews })
  }
  return [...totals].map(([country, metrics]) => ({ country, ...metrics }))
}
