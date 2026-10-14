import { useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'

import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { fmt } from '@/lib/format'
import { resolveRange, type Period } from '@/lib/search'
import { en } from '@/messages/en'

import { type Granularity } from '../pixels/buckets'
import { usePixelStats } from './api'
import { DailyBars } from './DailyBars'

const t = en.linksUi

/** URL segment for impressions without a campaign tag; "~" can never appear in a tag. */
export const untaggedSegment = '~untagged'

const periods: { value: Period; label: string }[] = [
  { value: '7d', label: t.last7 },
  { value: '30d', label: t.last30 },
  { value: '91d', label: t.last90 },
  { value: '6mo', label: t.last6mo },
  { value: '12mo', label: t.last12mo },
]

type Props = {
  org: string
  siteSlug: string
  siteId: number
  timezone: string
  /** null for every tag, '' for untagged impressions, otherwise one tag. */
  campaign: string | null
}

/** Period cards, chart and (for all tags) the campaign breakdown of the tracking pixel. */
export function PixelStatsSection({ org, siteSlug, siteId, timezone, campaign }: Props) {
  const [period, setPeriod] = useState<Period>('30d')
  const [granularity, setGranularity] = useState<Granularity>('day')
  const range = useMemo(() => resolveRange(period, timezone, new Date()), [period, timezone])
  const stats = usePixelStats(siteId, range, campaign)
  return (
    <section aria-labelledby="pixel-stats" className="flex flex-col gap-4 rounded-xl border bg-card p-4">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <h2 className="text-base font-medium" id="pixel-stats">{t.pixelStatsTitle}</h2>
        <div className="flex flex-wrap gap-3">
        <div className="flex flex-col gap-1">
          <Label htmlFor="pixel-group">{t.groupBy}</Label>
          <select
            id="pixel-group" className="min-h-11 rounded-lg border border-input bg-background px-3 text-sm" value={granularity}
            onChange={(event) => setGranularity(event.target.value as Granularity)}
          >
            <option value="day">{t.byDay}</option>
            <option value="week">{t.byWeek}</option>
            <option value="month">{t.byMonth}</option>
          </select>
        </div>
        <div className="flex flex-col gap-1">
          <Label htmlFor="pixel-period">{t.period}</Label>
          <select
            id="pixel-period" className="min-h-11 rounded-lg border border-input bg-background px-3 text-sm" value={period}
            onChange={(event) => setPeriod(event.target.value as Period)}
          >
            {periods.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
          </select>
        </div>
        </div>
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
          <dl className="grid grid-cols-2 gap-3 md:grid-cols-4" aria-label={t.pixelPeriodCards}>
            {([[t.pixelToday, stats.data.summary.today], [t.last7, stats.data.summary.last_7], [t.last30, stats.data.summary.last_30], [t.pixelAllTime, stats.data.summary.total]] as const).map(([label, value]) => (
              <div className="rounded-lg border p-3" key={label}>
                <dt className="text-xs text-muted-foreground">{label}</dt>
                <dd className="text-2xl font-semibold tabular-nums">{fmt.number(value)}</dd>
              </div>
            ))}
          </dl>
          <p className="text-xs text-muted-foreground">{t.pixelImpressions}: {fmt.number(stats.data.total)}</p>
          <DailyBars granularity={granularity} daily={stats.data.daily} from={stats.data.from} title={t.pixelStatsTitle} to={stats.data.to} unit={t.pixelStatsTitle} />
          {campaign === null && stats.data.campaigns.length > 0 ? (
            <div>
              <h3 className="mb-2 text-sm font-medium">{t.pixelCampaigns}</h3>
              <p className="mb-2 text-xs text-muted-foreground">{t.pixelCampaignHintOpen}</p>
              <ul className="flex flex-col gap-1 text-sm">
                {stats.data.campaigns.map((item) => (
                  <li className="border-b last:border-0" key={item.campaign || '(none)'}>
                    <Link
                      className="flex min-h-11 w-full items-baseline justify-between gap-3 rounded-md px-1 py-1.5 hover:bg-muted/50 focus-visible:outline-2 focus-visible:outline-ring"
                      params={{ org, site: siteSlug, campaign: item.campaign || untaggedSegment }}
                      to="/app/$org/$site/links/pixel/$campaign"
                    >
                      <span className="min-w-0 truncate">{item.campaign || t.pixelUntagged}</span>
                      <span className="tabular-nums">{fmt.number(item.impressions)}</span>
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
        </>
      ) : null}
    </section>
  )
}
