import { useState } from 'react'

import { Flag } from '@/components/Flag'
import { Button } from '@/components/ui/button'
import { countryLabel, normalizeCountryCode } from '@/lib/country'
import { fmt } from '@/lib/format'
import { cn } from '@/lib/utils'
import { en } from '@/messages/en'

import type { TrafficMetric } from './trafficGrid'

export type CountryMetricRow = { country: string; visitors: number; pageviews: number }

type CountryTableProps = {
  rows: CountryMetricRow[]
  metric: TrafficMetric
  selectedCountry?: string
  activeCountry?: string
  onActiveChange(country?: string): void
  onSelect(country: string): void
}

export function CountryTable({ rows, metric, selectedCountry, activeCountry, onActiveChange, onSelect }: CountryTableProps) {
  const [expanded, setExpanded] = useState(false)
  const normalized = rows
    .map((row) => ({ ...row, country: normalizeCountryCode(row.country) ?? '' }))
    .filter((row) => row.country && row[metric] > 0)
    .sort((left, right) => right[metric] - left[metric] || left.country.localeCompare(right.country))
  const total = normalized.reduce((sum, row) => sum + row[metric], 0)
  const metricLabel = metric === 'visitors' ? en.map.visitors.toLowerCase() : en.map.pageviews.toLowerCase()
  const visible = expanded ? normalized : normalized.slice(0, 6)

  return (
    <div className="min-w-0">
      <ol className="space-y-1" aria-label="Countries ranked by traffic">
        {visible.map((row, index) => {
          const value = row[metric]
          const share = total > 0 ? (value / total) * 100 : 0
          const label = countryLabel(row.country)
          const active = row.country === activeCountry
          return (
            <li key={row.country} className="grid grid-cols-[1.5rem_minmax(0,1fr)_auto] items-center gap-2">
              <span className="text-right text-xs tabular-nums text-muted-foreground" aria-hidden="true">{index + 1}</span>
              <button
                aria-label={`${label}, rank ${index + 1}, ${fmt.number(value)} ${metricLabel}, ${fmt.percent(share)}`}
                aria-pressed={row.country === selectedCountry}
                className={cn(
                  'min-w-0 rounded-md px-2 py-1.5 text-left outline-none transition-colors hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring',
                  active || row.country === selectedCountry ? 'bg-muted' : null,
                )}
                data-active={active || undefined}
                type="button"
                onBlur={() => onActiveChange(undefined)}
                onClick={() => onSelect(row.country)}
                onFocus={() => onActiveChange(row.country)}
                onMouseEnter={() => onActiveChange(row.country)}
                onMouseLeave={() => onActiveChange(undefined)}
              >
                <span className="flex min-w-0 items-center gap-2">
                  <Flag code={row.country} />
                  <span className="truncate text-sm">{label}</span>
                </span>
                <span aria-label={`${label} share`} className="mt-1 block h-1 overflow-hidden rounded-full bg-muted-foreground/15" data-share={share.toFixed(1)}>
                  <span aria-hidden="true" className="block h-full rounded-full bg-accent" style={{ width: `${share}%` }} />
                </span>
              </button>
              <span className="text-right text-sm tabular-nums">{fmt.number(value)}</span>
            </li>
          )
        })}
      </ol>
      {normalized.length > 6 ? (
        <Button className="mt-2 w-full" size="sm" variant="ghost" onClick={() => setExpanded((value) => !value)}>
          {expanded ? 'Show fewer countries' : 'Show all countries'}
        </Button>
      ) : null}
    </div>
  )
}
