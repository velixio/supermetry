import { countryName } from '@/lib/country'
import { fmt } from '@/lib/format'
import { en } from '@/messages/en'

import type { PixelStats } from './types'

const t = en.linksUi

function Rows({ title, rows }: { title: string; rows: { key: string; label: string; count: number }[] }) {
  return (
    <div>
      <h3 className="mb-2 text-sm font-medium">{title}</h3>
      {rows.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t.pixelPlacesNone}</p>
      ) : (
        <ul className="flex flex-col text-sm">
          {rows.map((row) => (
            <li className="flex items-baseline justify-between gap-3 border-b py-1.5 last:border-0" key={row.key}>
              <span className="min-w-0 truncate">{row.label}</span>
              <span className="tabular-nums">{fmt.number(row.count)}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/** Where the views came from: GitHub or not, and the country and city when the visitor was not behind a proxy. */
export function PixelPlaces({ stats }: { stats: Pick<PixelStats, 'sources' | 'countries' | 'cities'> }) {
  return (
    <section aria-labelledby="pixel-places" className="flex flex-col gap-3">
      <h2 className="text-base font-medium" id="pixel-places">{t.pixelPlacesTitle}</h2>
      <div className="grid gap-4 md:grid-cols-3">
        <Rows
          title={t.pixelSources}
          rows={stats.sources.map((row) => ({ key: row.source, label: row.source === 'github' ? t.sourceGitHub : t.sourceOther, count: row.impressions }))}
        />
        <Rows
          title={t.pixelCountries}
          rows={stats.countries.map((row) => ({ key: row.country, label: countryName(row.country), count: row.impressions }))}
        />
        <Rows
          title={t.pixelCities}
          rows={stats.cities.map((row) => ({ key: `${row.country}/${row.city}`, label: `${row.city}, ${countryName(row.country)}`, count: row.impressions }))}
        />
      </div>
      <p className="text-xs text-muted-foreground">{t.pixelPlacesNote}</p>
    </section>
  )
}
