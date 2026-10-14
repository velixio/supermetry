import { useMemo, useState } from 'react'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { fmt } from '@/lib/format'
import { resolveRange, type Period } from '@/lib/search'
import { en } from '@/messages/en'

import { usePixelSnippet, usePixelStats } from './api'
import { CopyButton } from './CopyButton'
import { DailyBars } from './DailyBars'
import { LinksTabs } from './LinksTabs'

const t = en.linksUi
const campaignPattern = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$/

const periods: { value: Period; label: string }[] = [
  { value: '7d', label: t.last7 },
  { value: '30d', label: t.last30 },
  { value: '91d', label: t.last90 },
]

type Props = { org: string; siteSlug: string; siteId: number; timezone: string }

export function PixelPage({ org, siteSlug, siteId, timezone }: Props) {
  const [campaign, setCampaign] = useState('')
  const [period, setPeriod] = useState<Period>('30d')
  const range = useMemo(() => resolveRange(period, timezone, new Date()), [period, timezone])
  const trimmed = campaign.trim()
  const valid = trimmed === '' || campaignPattern.test(trimmed)
  const snippet = usePixelSnippet(siteId, valid ? trimmed : '', valid)
  const stats = usePixelStats(siteId, range)

  return (
    <PageContainer width="narrow">
      <PageHeader title={t.title} description={t.pixelDescription} />
      <LinksTabs org={org} site={siteSlug} />

      <section aria-labelledby="pixel-snippet" className="flex flex-col gap-4 rounded-xl border bg-card p-4">
        <h2 className="text-base font-medium" id="pixel-snippet">{t.pixelTitle}</h2>
        <Field data-invalid={!valid}>
          <FieldLabel htmlFor="pixel-campaign">{t.pixelCampaign}</FieldLabel>
          <Input
            id="pixel-campaign" aria-invalid={!valid} autoCapitalize="none" autoComplete="off" className="min-h-11 max-w-sm"
            maxLength={64} spellCheck={false} value={campaign} onChange={(event) => setCampaign(event.target.value)}
          />
          <FieldDescription>{t.pixelCampaignHint}</FieldDescription>
          {!valid ? <FieldError>{t.pixelCampaignInvalid}</FieldError> : null}
        </Field>
        {snippet.isError ? <p role="alert" className="text-sm text-destructive">{snippet.error.message}</p> : null}
        {snippet.data && valid ? (
          <div className="flex flex-col gap-4">
            <div>
              <Label htmlFor="pixel-html">{t.pixelSnippet}</Label>
              <pre className="mt-1 overflow-x-auto rounded-lg border bg-muted/40 p-3 text-sm" id="pixel-html" tabIndex={0}><code>{snippet.data.html}</code></pre>
              <div className="mt-2"><CopyButton label={t.pixelCopySnippet} text={snippet.data.html}>{t.pixelCopySnippet}</CopyButton></div>
            </div>
            <div>
              <Label htmlFor="pixel-url">{t.pixelUrl}</Label>
              <p className="mt-1 break-all rounded-lg border bg-muted/40 p-3 font-mono text-sm" id="pixel-url">{snippet.data.url}</p>
              <div className="mt-2"><CopyButton label={t.pixelCopyUrl} text={snippet.data.url}>{t.pixelCopyUrl}</CopyButton></div>
            </div>
          </div>
        ) : null}
        <p className="text-xs text-muted-foreground">{t.pixelNote}</p>
      </section>

      <section aria-labelledby="pixel-stats" className="flex flex-col gap-4 rounded-xl border bg-card p-4">
        <div className="flex flex-wrap items-end justify-between gap-4">
          <h2 className="text-base font-medium" id="pixel-stats">{t.pixelStatsTitle}</h2>
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
        {stats.isPending ? <p className="text-sm text-muted-foreground" role="status">{t.loading}</p> : null}
        {stats.isError ? (
          <div className="flex items-center gap-3 text-sm" role="alert">
            {stats.error.message}
            <Button className="min-h-11" variant="outline" onClick={() => void stats.refetch()}>{t.retry}</Button>
          </div>
        ) : null}
        {stats.data ? (
          <>
            <dl>
              <dt className="text-xs text-muted-foreground">{t.pixelImpressions}</dt>
              <dd className="text-2xl font-semibold tabular-nums">{fmt.number(stats.data.total)}</dd>
            </dl>
            <DailyBars daily={stats.data.daily} from={stats.data.from} title={t.pixelStatsTitle} to={stats.data.to} unit={t.pixelStatsTitle} />
            {stats.data.campaigns.length > 0 ? (
              <div>
                <h3 className="mb-2 text-sm font-medium">{t.pixelCampaigns}</h3>
                <ul className="flex flex-col gap-1 text-sm">
                  {stats.data.campaigns.map((item) => (
                    <li className="flex items-baseline justify-between gap-3 border-b py-1.5 last:border-0" key={item.campaign || '(none)'}>
                      <span className="min-w-0 truncate">{item.campaign || t.pixelUntagged}</span>
                      <span className="tabular-nums">{fmt.number(item.impressions)}</span>
                    </li>
                  ))}
                </ul>
              </div>
            ) : null}
          </>
        ) : null}
      </section>
    </PageContainer>
  )
}
