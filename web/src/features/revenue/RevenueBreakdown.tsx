import { useEffect, useId, useState } from 'react'

import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { fmt } from '@/lib/format'
import { en } from '@/messages/en'

import {
  attributionKeys,
  breakdownKeys,
  clampLookback,
  downloadRevenueCsv,
  lookbackDefault,
  RevenueRequestError,
  useRevenueBreakdown,
  useRevenueCustomers,
  type RevenueAttribution,
  type RevenueBreakdownKey,
  type RevenueReportParams,
} from './revenueReportApi'

const t = en.revenueReport

type Props = {
  siteId: number
  orgId: number
  currency: string
  timezone: string
  from: string
  to: string
}

const selectClass = 'min-h-11 w-full rounded-lg border border-input bg-background px-2.5 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 md:text-sm'

function money(currency: string): Intl.NumberFormat {
  return new Intl.NumberFormat('en-US', { style: 'currency', currency, minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

function Stat({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return <div className="rounded-lg border bg-card p-3">
    <p className="text-xs text-muted-foreground">{label}</p>
    <p className="text-xl font-semibold tabular-nums">{value}</p>
    {hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
  </div>
}

function useDebounced(value: string, delay = 300): string {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delay)
    return () => clearTimeout(timer)
  }, [value, delay])
  return debounced
}

function Customers({ siteId, orgId, currency, timezone, from, to }: Props) {
  const formatter = money(currency)
  const [search, setSearch] = useState('')
  const debounced = useDebounced(search.trim())
  const query = useRevenueCustomers(siteId, orgId, from, to, debounced, true)
  const searchId = useId()
  const status = query.error instanceof RevenueRequestError ? query.error.status : 0
  let body
  if (query.isPending) body = <p className="text-sm text-muted-foreground" role="status">{t.customersLoading}</p>
  else if (status === 403) body = <p className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground" role="status">{t.customersForbidden}</p>
  else if (status === 400 || status === 404 || status === 409) body = <p className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground" role="status">{t.customersUnavailable}</p>
  else if (query.isError) body = <ErrorState detail={query.error.message} onRetry={() => void query.refetch()} />
  else if (query.data.length === 0) body = <p className="text-sm text-muted-foreground" role="status">{t.customersEmpty}</p>
  else body = <div className="overflow-x-auto">
    <table className="w-full min-w-96 text-sm">
      <caption className="sr-only">{t.customersTitle}</caption>
      <thead><tr className="border-b text-left text-muted-foreground">
        <th className="pb-2 font-medium" scope="col">{t.customer}</th>
        <th className="pb-2 text-right font-medium" scope="col">{t.orders}</th>
        <th className="pb-2 text-right font-medium" scope="col">{t.revenue}</th>
        <th className="pb-2 text-right font-medium" scope="col">{t.firstOrder}</th>
        <th className="pb-2 text-right font-medium" scope="col">{t.lastOrder}</th>
      </tr></thead>
      <tbody>{query.data.map((customer) => <tr className="border-b last:border-0" key={customer.profile_id}>
        <th className="max-w-48 truncate py-2 text-left font-normal" scope="row" title={customer.external_id}>{customer.external_id}</th>
        <td className="py-2 text-right tabular-nums">{fmt.number(customer.orders)}</td>
        <td className="py-2 text-right tabular-nums">{formatter.format(customer.revenue)}</td>
        <td className="py-2 text-right tabular-nums">{fmt.date(customer.first_order_at, timezone)}</td>
        <td className="py-2 text-right tabular-nums">{fmt.date(customer.last_order_at, timezone)}</td>
      </tr>)}</tbody>
    </table>
  </div>
  return <Card aria-busy={query.isPending} aria-labelledby={`${searchId}-title`}>
    <CardHeader>
      <CardTitle id={`${searchId}-title`}>{t.customersTitle}</CardTitle>
      <CardDescription>{t.customersDescription}</CardDescription>
    </CardHeader>
    <CardContent className="space-y-3">
      {status === 403 || status === 400 || status === 404 || status === 409 ? null : <div className="max-w-sm">
        <label className="mb-1 block text-sm font-medium" htmlFor={searchId}>{t.customersSearch}</label>
        <Input id={searchId} className="min-h-11" type="search" maxLength={100} value={search} placeholder={t.customersSearchPlaceholder} onChange={(event) => setSearch(event.target.value)} />
      </div>}
      {body}
    </CardContent>
  </Card>
}

export function RevenueBreakdown(props: Props) {
  const { siteId, orgId, currency, from, to } = props
  const [breakdown, setBreakdown] = useState<RevenueBreakdownKey>('source')
  const [attribution, setAttribution] = useState<RevenueAttribution>('event')
  const [lookbackText, setLookbackText] = useState(String(lookbackDefault))
  const [exportState, setExportState] = useState<'idle' | 'busy' | 'failed' | 'forbidden'>('idle')
  const ids = { breakdown: useId(), attribution: useId(), lookback: useId(), hint: useId() }
  const lookbackNumber = Number(lookbackText)
  const lookbackValid = lookbackText.trim() !== '' && Number.isInteger(lookbackNumber) && lookbackNumber >= 1 && lookbackNumber <= 90
  const params: RevenueReportParams = { from, to, breakdown, attribution, lookbackDays: lookbackValid ? lookbackNumber : clampLookback(lookbackNumber) }
  const query = useRevenueBreakdown(siteId, orgId, params)
  const formatter = money(currency)

  async function exportCsv() {
    setExportState('busy')
    try {
      await downloadRevenueCsv(siteId, orgId, params)
      setExportState('idle')
    } catch (error) {
      setExportState(error instanceof RevenueRequestError && error.status === 403 ? 'forbidden' : 'failed')
    }
  }

  const status = query.error instanceof RevenueRequestError ? query.error.status : 0
  const data = query.data
  const rows = data?.rows ?? []
  const maximum = Math.max(1, ...rows.map((row) => row.revenue))
  const label = (key: string) => key === '' ? t.direct : key
  const top = rows[0]

  let body
  if (query.isPending) body = <p className="text-sm text-muted-foreground" role="status">{t.loading}</p>
  else if (status === 409) body = <p className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground" role="status">{t.currencyConflict}</p>
  else if (status === 403) body = <p className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground" role="status">{t.forbidden}</p>
  else if (query.isError) body = <ErrorState detail={query.error.message} onRetry={() => void query.refetch()} />
  else if (!data) body = null
  else body = <>
    {data.truncated ? <p className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground" role="status">{t.truncated}</p> : null}
    <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
      <Stat label={t.orders} value={fmt.number(data.orders)} />
      <Stat label={t.averageOrderValue} value={formatter.format(data.average_order_value)} />
      <Stat label={t.revenuePerVisitor} value={formatter.format(data.revenue_per_visitor)} hint={`${fmt.number(data.visitors)} ${t.visitors.toLowerCase()}`} />
      {data.unique_customers !== undefined ? <Stat label={t.uniqueCustomers} value={fmt.number(data.unique_customers)} /> : null}
      {data.repeat_rate !== undefined ? <Stat label={t.repeatRate} value={`${fmt.number(data.repeat_rate)}%`} hint={data.repeat_customers !== undefined ? `${fmt.number(data.repeat_customers)} ${t.repeatCustomers.toLowerCase()}` : undefined} /> : null}
    </div>
    {rows.length === 0 ? <p className="text-sm text-muted-foreground" role="status">{t.empty}</p> : <figure className="space-y-2">
      <figcaption className="text-xs text-muted-foreground">{top ? t.summary(rows.length, t.breakdowns[breakdown], `${label(top.key)} (${formatter.format(top.revenue)})`) : ''}</figcaption>
      <div className="overflow-x-auto">
        <table className="w-full min-w-96 text-sm">
          <caption className="sr-only">{`${t.title}: ${t.breakdowns[breakdown]}`}</caption>
          <thead><tr className="border-b text-left text-muted-foreground">
            <th className="pb-2 font-medium" scope="col">{t.breakdowns[breakdown]}</th>
            <th className="pb-2 text-right font-medium" scope="col">{t.orders}</th>
            <th className="pb-2 text-right font-medium" scope="col">{t.revenue}</th>
            <th className="pb-2 text-right font-medium" scope="col">{t.averageOrderValue}</th>
            {data.unique_customers !== undefined ? <th className="pb-2 text-right font-medium" scope="col">{t.uniqueCustomers}</th> : null}
            {data.repeat_rate !== undefined ? <th className="pb-2 text-right font-medium" scope="col">{t.repeatRate}</th> : null}
          </tr></thead>
          <tbody>{rows.map((row) => <tr className="border-b last:border-0" key={row.key}>
            <th className="max-w-56 py-2 text-left font-normal" scope="row">
              <span className="block truncate" title={label(row.key)}>{label(row.key)}</span>
              <span aria-hidden="true" className="mt-1 block h-1.5 rounded bg-primary/75" style={{ width: `${Math.max(2, row.revenue / maximum * 100)}%` }} />
            </th>
            <td className="py-2 text-right tabular-nums">{fmt.number(row.orders)}</td>
            <td className="py-2 text-right tabular-nums">{formatter.format(row.revenue)}</td>
            <td className="py-2 text-right tabular-nums">{formatter.format(row.average_order_value)}</td>
            {data.unique_customers !== undefined ? <td className="py-2 text-right tabular-nums">{fmt.number(row.unique_customers ?? 0)}</td> : null}
            {data.repeat_rate !== undefined ? <td className="py-2 text-right tabular-nums">{row.repeat_rate === undefined ? '' : `${fmt.number(row.repeat_rate)}%`}</td> : null}
          </tr>)}</tbody>
        </table>
      </div>
    </figure>}
  </>

  return <section className="flex min-w-0 flex-col gap-4" aria-labelledby={`${ids.breakdown}-title`}>
    <Card aria-busy={query.isPending}>
      <CardHeader>
        <CardTitle id={`${ids.breakdown}-title`}>{t.title}</CardTitle>
        <CardDescription>{t.description}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid items-end gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <div>
            <label className="mb-1 block text-sm font-medium" htmlFor={ids.breakdown}>{t.breakdown}</label>
            <select id={ids.breakdown} className={selectClass} value={breakdown} onChange={(event) => setBreakdown(event.target.value as RevenueBreakdownKey)}>
              {breakdownKeys.map((key) => <option key={key} value={key}>{t.breakdowns[key]}</option>)}
            </select>
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium" htmlFor={ids.attribution}>{t.attribution}</label>
            <select id={ids.attribution} className={selectClass} value={attribution} onChange={(event) => setAttribution(event.target.value as RevenueAttribution)}>
              {attributionKeys.map((key) => <option key={key} value={key}>{t.attributions[key]}</option>)}
            </select>
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium" htmlFor={ids.lookback}>{t.lookback}</label>
            <Input id={ids.lookback} className="min-h-11" type="number" inputMode="numeric" min={1} max={90} step={1} value={lookbackText}
              disabled={attribution === 'event'} aria-invalid={!lookbackValid} aria-describedby={ids.hint} onChange={(event) => setLookbackText(event.target.value)} />
            <p className={`mt-1 text-xs ${lookbackValid ? 'text-muted-foreground' : 'text-destructive'}`} id={ids.hint}>{t.lookbackHint}</p>
          </div>
          <div>
            <Button type="button" variant="outline" className="min-h-11 w-full" disabled={exportState === 'busy' || !data} onClick={() => void exportCsv()}>
              {exportState === 'busy' ? t.exporting : t.exportCsv}
            </Button>
          </div>
        </div>
        {exportState === 'failed' || exportState === 'forbidden'
          ? <p className="text-sm text-destructive" role="alert">{exportState === 'forbidden' ? t.exportForbidden : t.exportFailed}</p>
          : null}
        {body}
      </CardContent>
    </Card>
    {data?.unique_customers !== undefined ? <Customers {...props} /> : null}
  </section>
}
