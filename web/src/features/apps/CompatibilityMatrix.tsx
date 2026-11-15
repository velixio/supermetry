import { Link } from '@tanstack/react-router'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { fmt } from '@/lib/format'

import { AppFilters } from './AppFilters'
import { useAppCompatibility } from './api'
import { formatRate } from './format'
import { APP_PLATFORMS, type AppSearch } from './search'

export type Tone = 'good' | 'warn' | 'bad' | 'none'

/** Crash-free users bands: 99% and above is healthy, 95% to 99% needs a look, below 95% is bad. */
export function crashFreeTone(rate: number | null): Tone {
  if (rate === null) return 'none'
  return rate >= 0.99 ? 'good' : rate >= 0.95 ? 'warn' : 'bad'
}

const TONE_COLOR: Record<Exclude<Tone, 'none'>, string> = { good: 'var(--chart-2)', warn: 'var(--chart-4)', bad: 'var(--chart-1)' }

export function CompatibilityMatrix({ org, site, siteId, timezone, search, onSearch }: {
  org: string; site: string; siteId: number; timezone: string; search: AppSearch; onSearch: (next: AppSearch) => void
}) {
  const compat = useAppCompatibility(siteId, search, timezone)
  const chips: [string, string | undefined][] = [['All platforms', undefined], ...APP_PLATFORMS.map((value): [string, string] => [value, value])]
  return (
    <PageContainer>
      <PageHeader title="Compatibility" description="Crashes and installs by platform, OS version, architecture and device class." actions={<AppFilters siteId={siteId} timezone={timezone} search={search} onSearch={onSearch} hidePlatform />} />
      <div className="flex flex-wrap gap-2" role="group" aria-label="Platform">
        {chips.map(([label, value]) => (
          <Button key={label} type="button" size="sm" variant={search.platform === value ? 'default' : 'outline'} aria-pressed={search.platform === value} onClick={() => onSearch({ ...search, platform: value })}>{label}</Button>
        ))}
      </div>
      {compat.isPending ? <p role="status" className="text-sm text-muted-foreground">Loading compatibility…</p> : null}
      {compat.isError ? <p role="alert" className="text-sm text-destructive">{compat.error.message}</p> : null}
      {compat.data && compat.data.rows.length === 0 ? <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">No reports in this period.</p> : null}
      {compat.data && compat.data.rows.length > 0 ? (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Platform</TableHead><TableHead>OS version</TableHead><TableHead>Arch</TableHead><TableHead>Device</TableHead>
              <TableHead className="text-right">Events</TableHead><TableHead className="text-right">Crashes</TableHead><TableHead className="text-right">Installs</TableHead>
              <TableHead className="text-right">Crash-free users</TableHead><TableHead>Top issues</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {compat.data.rows.map((row) => {
              const tone = crashFreeTone(row.crash_free_users)
              return (
                <TableRow key={`${row.platform}/${row.os_version}/${row.arch}/${row.device_class}`}>
                  <TableCell>{row.platform}</TableCell><TableCell>{row.os_version}</TableCell><TableCell>{row.arch}</TableCell><TableCell>{row.device_class}</TableCell>
                  <TableCell className="text-right tabular-nums">{fmt.number(row.events)}</TableCell>
                  <TableCell className="text-right tabular-nums">{fmt.number(row.crashes)}</TableCell>
                  <TableCell className="text-right tabular-nums">{fmt.number(row.installs)}</TableCell>
                  <TableCell className="text-right">
                    <span data-tone={tone} className="inline-block rounded px-2 py-0.5 tabular-nums" style={tone === 'none' ? undefined : { backgroundColor: `color-mix(in oklab, ${TONE_COLOR[tone]} 30%, transparent)` }}>{formatRate(row.crash_free_users)}</span>
                  </TableCell>
                  <TableCell className="whitespace-normal">
                    <ul className="flex flex-col gap-1">
                      {row.top_issues.map((issue) => (
                        <li key={issue.id}><Link className="text-sm underline-offset-2 hover:underline" to="/app/$org/$site/issues/$issue" params={{ org, site, issue: String(issue.id) }}>{issue.title}</Link></li>
                      ))}
                    </ul>
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      ) : null}
    </PageContainer>
  )
}
