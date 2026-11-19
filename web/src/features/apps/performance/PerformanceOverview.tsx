import { Link } from '@tanstack/react-router'

import { InfoTip } from '@/components/InfoTip'
import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { fmt } from '@/lib/format'

import { AppFilters } from '../AppFilters'
import { formatMs, formatRate } from '../format'
import type { AppSearch } from '../search'
import { useNetworkFailures, usePerformanceOverview, useTransactions } from './api'
import { RegressionsCard } from './RegressionsCard'
import { SpanGroupsTable } from './TransactionDetail'
import { TransactionsTable } from './TransactionsTable'

function Tile({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <Card>
      <CardHeader>
        <CardDescription>{label}</CardDescription>
        <CardTitle className="text-2xl tabular-nums">{children}</CardTitle>
      </CardHeader>
    </Card>
  )
}

const vital = (value: number | undefined, unit = 'ms') => (value === undefined ? 'n/a' : unit === '' ? value.toFixed(3) : `${Math.round(value)} ${unit}`)

/** Performance landing page: totals, slowest and failing transactions, app start, network failures and regressions. */
export function PerformanceOverview({ org, site, siteId, timezone, search, onSearch }: {
  org: string; site: string; siteId: number; timezone: string; search: AppSearch; onSearch: (next: AppSearch) => void
}) {
  const overview = usePerformanceOverview(siteId, search, timezone)
  const network = useNetworkFailures(siteId, search, timezone)
  const transactions = useTransactions(siteId, search, timezone)
  const header = <PageHeader title="Performance" description="Transaction latency, errors and network calls." actions={<AppFilters siteId={siteId} timezone={timezone} search={search} onSearch={onSearch} />} />
  if (overview.isPending) return <PageContainer>{header}<p role="status" className="text-sm text-muted-foreground">Loading performance…</p></PageContainer>
  if (overview.isError) return <PageContainer>{header}<p role="alert" className="text-sm text-destructive">{overview.error.message}</p></PageContainer>
  const data = overview.data
  if (data.transactions === 0) {
    return (
      <PageContainer>
        {header}
        <div className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">
          <p>No transactions in this period.</p>
          <p className="mt-1">Set <code>tracesSampleRate</code> in a Sentry SDK, or send OpenTelemetry traces. <Link className="font-medium text-foreground underline" to="/app/$org/$site/settings/installation" params={{ org, site }}>Setup instructions</Link></p>
        </div>
      </PageContainer>
    )
  }
  const link = data.vitals_link
  return (
    <PageContainer>
      {header}
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4" aria-label="Key figures">
        <Tile label="Transactions">{fmt.number(data.transactions)}</Tile>
        <Tile label="p50">{formatMs(data.p50_ms)}</Tile>
        <Tile label="p95">{formatMs(data.p95_ms)}</Tile>
        <Tile label="Error rate">{formatRate(data.error_rate)}</Tile>
      </div>
      {link ? (
        <Card>
          <CardHeader><CardTitle>Web Vitals <InfoTip label="About web vitals">75th percentile on the linked website.</InfoTip></CardTitle></CardHeader>
          <CardContent className="flex flex-wrap items-center gap-6 text-sm">
            <span>LCP {vital(link.lcp_p75)}</span>
            <span>INP {vital(link.inp_p75)}</span>
            <span>CLS {vital(link.cls_p75, '')}</span>
            <Link className="font-medium underline" to="/app/$org/$site/vitals" params={{ org, site: String(link.site_id) }} search={{ period: search.period } as never}>Open Web Vitals</Link>
          </CardContent>
        </Card>
      ) : null}
      {data.app_start.length > 0 ? (
        <Card>
          <CardHeader><CardTitle>App start</CardTitle></CardHeader>
          <CardContent>
            <ul className="flex flex-wrap gap-6 text-sm" aria-label="App start">
              {data.app_start.map((start) => <li key={start.type}><span className="capitalize">{start.type}</span> start: p50 {formatMs(start.p50_ms)}, p95 {formatMs(start.p95_ms)}</li>)}
            </ul>
          </CardContent>
        </Card>
      ) : null}
      <Card>
        <CardHeader><CardTitle>Transactions <InfoTip label="About transactions">Slowest first; click a column to sort.</InfoTip></CardTitle></CardHeader>
        <CardContent>
          {transactions.isError ? <p role="alert" className="text-sm text-destructive">{transactions.error.message}</p> : null}
          <TransactionsTable org={org} site={site} rows={transactions.data?.transactions ?? data.slowest} search={search} />
        </CardContent>
      </Card>
      {data.most_failing.length > 0 ? (
        <Card>
          <CardHeader><CardTitle>Most failing transactions</CardTitle><CardDescription>By error rate.</CardDescription></CardHeader>
          <CardContent><TransactionsTable org={org} site={site} rows={data.most_failing} search={search} label="Most failing transactions" /></CardContent>
        </Card>
      ) : null}
      {network.data && network.data.failures.length > 0 ? (
        <Card>
          <CardHeader><CardTitle>Network failures <InfoTip label="About network failures">Outgoing HTTP calls that failed, by parameterised URL.</InfoTip></CardTitle></CardHeader>
          <CardContent><SpanGroupsTable groups={network.data.failures} label="Network failures" /></CardContent>
        </Card>
      ) : null}
      <RegressionsCard org={org} site={site} siteId={siteId} timezone={timezone} search={search} />
    </PageContainer>
  )
}
