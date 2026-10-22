import { useState } from 'react'

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { ErrorState } from '@/components/ErrorState'
import { fmt } from '@/lib/format'
import { en } from '@/messages/en'
import { AdminPage, formatDate } from './AdminCommon'
import {
  isNotConfigured, useStarsAnnotations, useStarsSeries, useStarsSummary,
  type StarsGranularity, type StarsSummary,
} from './githubStarsApi'
import { StarsAnnotations } from './StarsAnnotations'
import { StarsChart } from './StarsChart'
import { StarsEvents } from './StarsEvents'

function errorText(error: unknown) {
  return error instanceof Error ? error.message : undefined
}

function signed(value: number) {
  return `${value > 0 ? '+' : ''}${fmt.number(value)}`
}

function Counter({ label, value, live }: { label: string; value: string; live?: boolean }) {
  return (
    <Card className="min-w-0">
      <CardHeader><CardTitle className="text-sm font-medium text-muted-foreground">{label}</CardTitle></CardHeader>
      <CardContent><p className="text-2xl font-semibold tabular-nums" aria-live={live ? 'polite' : undefined}>{value}</p></CardContent>
    </Card>
  )
}

function Counters({ summary }: { summary: StarsSummary }) {
  return (
    <section aria-label={en.githubStars.liveCounter} className="flex flex-col gap-2">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3">
        <Counter live label={en.githubStars.liveCounter} value={fmt.number(summary.stars)} />
        <Counter label={en.githubStars.net24h} value={signed(summary.net_24h)} />
        <Counter label={en.githubStars.net7d} value={signed(summary.net_7d)} />
        <Counter label={en.githubStars.forks} value={fmt.number(summary.forks)} />
        <Counter label={en.githubStars.watchers} value={fmt.number(summary.watchers)} />
        <Counter label={en.githubStars.openIssues} value={fmt.number(summary.open_issues)} />
      </div>
      <p className="text-xs text-muted-foreground">{summary.updated_at ? en.githubStars.updatedAt(formatDate(summary.updated_at)) : en.githubStars.notUpdated}</p>
    </section>
  )
}

function GrowthChart() {
  const [granularity, setGranularity] = useState<StarsGranularity>('day')
  const series = useStarsSeries(granularity)
  const annotations = useStarsAnnotations()
  return (
    <section aria-labelledby="stars-chart" className="flex min-w-0 flex-col gap-3">
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div className="flex flex-col gap-1">
          <h2 id="stars-chart" className="text-base font-semibold">{en.githubStars.chartTitle}</h2>
          <p className="text-xs text-muted-foreground">
            {granularity === 'hour' ? en.githubStars.rangeHour : en.githubStars.rangeDay}
            {series.data ? ` · ${en.githubStars.timeZone(series.data.time_zone)}` : ''}
          </p>
        </div>
        <ToggleGroup aria-label={en.githubStars.granularity} type="single" value={granularity} onValueChange={(value) => (value === 'hour' || value === 'day') && setGranularity(value)}>
          <ToggleGroupItem className="h-11 min-w-11" value="hour">{en.githubStars.hour}</ToggleGroupItem>
          <ToggleGroupItem className="h-11 min-w-11" value="day">{en.githubStars.day}</ToggleGroupItem>
        </ToggleGroup>
      </div>
      {series.isPending ? <Skeleton aria-label={en.githubStars.loading} className="h-64 w-full" /> : series.isError ? (
        <ErrorState detail={errorText(series.error)} onRetry={() => void series.refetch()} />
      ) : (
        <StarsChart points={series.data.data} granularity={granularity} timeZone={series.data.time_zone} annotations={annotations.data?.data ?? []} />
      )}
      <StarsAnnotations annotations={annotations.data?.data ?? []} />
    </section>
  )
}

export function AdminGitHubStarsPage() {
  const summary = useStarsSummary()
  return (
    <AdminPage title={en.githubStars.title} description={en.githubStars.description}>
      {summary.isPending ? <Skeleton aria-label={en.githubStars.loading} className="h-40 w-full" /> : isNotConfigured(summary.error) ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>{en.githubStars.notConfiguredTitle}</EmptyTitle>
            <EmptyDescription>{en.githubStars.notConfiguredBody}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : summary.isError ? (
        <ErrorState level="page" detail={errorText(summary.error)} onRetry={() => void summary.refetch()} />
      ) : (
        <>
          <Counters summary={summary.data} />
          <GrowthChart />
          <StarsEvents />
        </>
      )}
    </AdminPage>
  )
}
