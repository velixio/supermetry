import { useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { ArrowLeftIcon } from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { countryName } from '@/lib/country'
import { fmt } from '@/lib/format'
import { resolveRange, type Period } from '@/lib/search'
import { en } from '@/messages/en'

import { useLink, useLinkStats } from './api'
import { CopyButton } from './CopyButton'
import { DailyBars } from './DailyBars'
import { statusVariant, absoluteShortUrl } from './LinksPage'
import type { LinkCount } from './types'

const t = en.linksUi

const periods: { value: Period; label: string }[] = [
  { value: '7d', label: t.last7 },
  { value: '30d', label: t.last30 },
  { value: '91d', label: t.last90 },
]

type Props = { org: string; siteSlug: string; siteId: number; linkId: number; timezone: string }

export function LinkStatsPage({ org, siteSlug, siteId, linkId, timezone }: Props) {
  const [period, setPeriod] = useState<Period>('30d')
  const range = useMemo(() => resolveRange(period, timezone, new Date()), [period, timezone])
  const link = useLink(siteId, linkId)
  const stats = useLinkStats(linkId, range)

  return (
    <div className="mx-auto flex w-full max-w-5xl min-w-0 flex-col gap-6">
      <Button asChild className="min-h-11 self-start" variant="ghost">
        <Link params={{ org, site: siteSlug }} to="/app/$org/$site/links">
          <ArrowLeftIcon data-icon="inline-start" />
          {t.back}
        </Link>
      </Button>
      {link.isError ? <p role="alert" className="text-sm text-destructive">{link.error.message}</p> : null}
      {link.data ? (
        <header className="flex flex-wrap items-start justify-between gap-4">
          <div className="min-w-0">
            <h1 className="flex flex-wrap items-center gap-2 text-2xl font-semibold tracking-tight">
              <span className="min-w-0 break-words">{link.data.name}</span>
              <Badge variant={statusVariant(link.data.status)}>{t.status[link.data.status]}</Badge>
            </h1>
            <p className="mt-1 break-all font-mono text-sm">{absoluteShortUrl(link.data)}</p>
            <p className="mt-1 break-all text-sm text-muted-foreground">{link.data.destination}</p>
          </div>
          <CopyButton label={t.copyShortUrl.replace('{name}', link.data.name)} text={absoluteShortUrl(link.data)} />
        </header>
      ) : (
        <h1 className="text-2xl font-semibold tracking-tight">{t.statsTitle}</h1>
      )}

      <div className="flex flex-wrap items-end gap-4">
        <div className="flex flex-col gap-1">
          <Label htmlFor="link-period">{t.period}</Label>
          <select
            id="link-period"
            className="min-h-11 rounded-lg border border-input bg-background px-3 text-sm"
            value={period}
            onChange={(event) => setPeriod(event.target.value as Period)}
          >
            {periods.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
          </select>
        </div>
        <dl className="flex gap-6">
          <div>
            <dt className="text-xs text-muted-foreground">{t.totalClicks}</dt>
            <dd className="text-2xl font-semibold tabular-nums">{stats.data ? fmt.number(stats.data.total) : '—'}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">{t.allTimeClicks}</dt>
            <dd className="text-2xl font-semibold tabular-nums">{link.data ? fmt.number(link.data.clicks) : '—'}</dd>
          </div>
        </dl>
      </div>

      {stats.isPending ? <p className="text-sm text-muted-foreground" role="status">{t.loading}</p> : null}
      {stats.isError ? (
        <div className="flex items-center gap-3 text-sm" role="alert">
          {stats.error.message}
          <Button className="min-h-11" variant="outline" onClick={() => void stats.refetch()}>{t.retry}</Button>
        </div>
      ) : null}
      {stats.data ? (
        <>
          <section aria-labelledby="link-daily" className="rounded-xl border bg-card p-4">
            <h2 className="mb-3 text-base font-medium" id="link-daily">{t.dailyTitle}</h2>
            <DailyBars daily={stats.data.daily} from={stats.data.from} title={t.dailyTitle} to={stats.data.to} unit={t.columns.clicks} />
          </section>
          <div className="grid gap-4 md:grid-cols-3">
            <Breakdown
              items={stats.data.countries}
              label={(value) => (value ? countryName(value) : t.unknownCountry)}
              title={t.countries}
              total={stats.data.total}
            />
            <Breakdown items={stats.data.referrers} label={(value) => value || t.direct} title={t.referrers} total={stats.data.total} />
            <Breakdown items={stats.data.devices} label={(value) => value.charAt(0).toUpperCase() + value.slice(1)} title={t.devices} total={stats.data.total} />
          </div>
          <p className="text-xs text-muted-foreground">{t.statsNote}</p>
        </>
      ) : null}
    </div>
  )
}

function Breakdown({ title, items, total, label }: {
  title: string
  items: LinkCount[]
  total: number
  label: (value: string) => string
}) {
  const heading = `link-breakdown-${title.toLowerCase()}`
  return (
    <section aria-labelledby={heading} className="min-w-0 rounded-xl border bg-card p-4">
      <h2 className="mb-3 text-base font-medium" id={heading}>{title}</h2>
      {items.length === 0 ? <p className="text-sm text-muted-foreground">{t.noData}</p> : (
        <ul className="flex flex-col gap-3">
          {items.map((item) => {
            const share = total > 0 ? Math.round((item.clicks / total) * 100) : 0
            return (
              <li key={item.value || '(none)'}>
                <div className="flex items-baseline justify-between gap-3 text-sm">
                  <span className="min-w-0 truncate" title={label(item.value)}>{label(item.value)}</span>
                  <span className="shrink-0 tabular-nums">{fmt.number(item.clicks)} <span className="text-muted-foreground">({share}%)</span></span>
                </div>
                <div aria-hidden="true" className="mt-1 h-1.5 rounded-full bg-muted">
                  <div className="h-full rounded-full bg-primary" style={{ width: `${share}%` }} />
                </div>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
