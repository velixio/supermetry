import { Flag } from '@/components/Flag'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { countryLabel, normalizeCountryCode } from '@/lib/country'
import { fmt } from '@/lib/format'
import { cn } from '@/lib/utils'
import { en } from '@/messages/en'

import type { TrafficMetric } from './trafficGrid'

export type CountryMetricRow = {
  country: string
  visitors: number
  pageviews: number
}

type CountryTableProps = {
  rows: CountryMetricRow[]
  metric: TrafficMetric
  selectedCountry?: string
  onSelect(country: string): void
}

export function CountryTable({ rows, metric, selectedCountry, onSelect }: CountryTableProps) {
  const normalized = rows
    .map((row) => ({
      ...row,
      country: normalizeCountryCode(row.country) ?? '',
    }))
    .filter((row) => row.country && row[metric] > 0)
    .sort((left, right) => right[metric] - left[metric])
  const total = normalized.reduce((sum, row) => sum + row[metric], 0)
  const maximum = normalized[0]?.[metric] ?? 0
  const label = metric === 'visitors' ? en.map.visitors : en.map.pageviews

  return (
    <Card className="min-w-0">
      <CardHeader>
        <CardTitle>{en.map.countries}</CardTitle>
      </CardHeader>
      <CardContent className="min-w-0 p-0">
        <div className="overflow-hidden">
          <table className="w-full table-fixed text-sm">
            <thead className="border-y text-left text-xs text-muted-foreground">
              <tr>
                <th className="w-1/2 px-4 py-2 font-medium" scope="col">
                  {en.map.country}
                </th>
                <th className="w-1/4 px-2 py-2 text-right font-medium" scope="col">
                  {label}
                </th>
                <th className="w-1/4 px-4 py-2 text-right font-medium" scope="col">
                  {en.map.share}
                </th>
              </tr>
            </thead>
            <tbody>
              {normalized.map((row) => {
                const value = row[metric]
                const share = total > 0 ? (value / total) * 100 : 0
                return (
                  <tr key={row.country} className="border-b last:border-0">
                    <th className="min-w-0 p-1 text-left font-normal" scope="row">
                      <button
                        aria-pressed={row.country === selectedCountry}
                        className={cn(
                          'relative flex w-full min-w-0 items-center gap-2 overflow-hidden rounded-md px-3 py-2 text-left outline-none hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring',
                          row.country === selectedCountry ? 'bg-muted' : null,
                        )}
                        type="button"
                        onClick={() => onSelect(row.country)}
                      >
                        <span
                          aria-hidden="true"
                          className="absolute inset-y-1 left-0 rounded-r bg-accent/15"
                          style={{
                            width: maximum > 0 ? `${(value / maximum) * 100}%` : 0,
                          }}
                        />
                        <span className="relative flex min-w-0 items-center gap-2">
                          <Flag code={row.country} />
                          <span className="truncate">{countryLabel(row.country)}</span>
                        </span>
                      </button>
                    </th>
                    <td className="px-2 py-2 text-right tabular-nums">{fmt.number(value)}</td>
                    <td className="px-4 py-2 text-right tabular-nums text-muted-foreground">{fmt.percent(share)}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      </CardContent>
    </Card>
  )
}
