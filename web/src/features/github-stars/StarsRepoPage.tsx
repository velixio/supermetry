import { Link } from '@tanstack/react-router'
import { ChevronLeftIcon } from 'lucide-react'
import { useState } from 'react'

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { ErrorState } from '@/components/ErrorState'
import { fmt } from '@/lib/format'
import { en } from '@/messages/en'
import { formatDate } from '@/features/admin/AdminCommon'
import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import {
  useStarsAnnotations, useStarsRepos, useStarsSeries, useStarsSummary,
  type StarsGranularity, type StarsSummary,
} from './githubStarsApi'
import { RepoPanel, RevealPanel, type Reveal } from './StarsRepos'
import { StarsAnnotations } from './StarsAnnotations'
import { StarsChart } from './StarsChart'
import { StarsEvents } from './StarsEvents'

function errorText(error: unknown) {
  return error instanceof Error ? error.message : undefined
}

function signed(value: number) {
  return `${value > 0 ? '+' : ''}${fmt.full(value)}`
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
        <Counter live label={en.githubStars.liveCounter} value={fmt.full(summary.stars)} />
        <Counter label={en.githubStars.net24h} value={signed(summary.net_24h)} />
        <Counter label={en.githubStars.net7d} value={signed(summary.net_7d)} />
        <Counter label={en.githubStars.forks} value={fmt.full(summary.forks)} />
        <Counter label={en.githubStars.watchers} value={fmt.full(summary.watchers)} />
        <Counter label={en.githubStars.openIssues} value={fmt.full(summary.open_issues)} />
      </div>
      <p className="text-xs text-muted-foreground">{summary.updated_at ? en.githubStars.updatedAt(formatDate(summary.updated_at)) : en.githubStars.notUpdated}</p>
    </section>
  )
}

function GrowthChart({ repoId }: { repoId: number }) {
  const [granularity, setGranularity] = useState<StarsGranularity>('day')
  const series = useStarsSeries(repoId, granularity)
  const annotations = useStarsAnnotations(repoId)
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
      <StarsAnnotations repoId={repoId} annotations={annotations.data?.data ?? []} />
    </section>
  )
}

function RepoView({ repoId }: { repoId: number }) {
  const summary = useStarsSummary(repoId)
  return (
    <>
      {summary.isPending ? <Skeleton aria-label={en.githubStars.loading} className="h-40 w-full" /> : summary.isError ? (
        <ErrorState level="page" detail={errorText(summary.error)} onRetry={() => void summary.refetch()} />
      ) : <Counters summary={summary.data} />}
      <GrowthChart repoId={repoId} />
      <StarsEvents repoId={repoId} />
    </>
  )
}

/** One repository's page: status and actions, counters, chart, annotations and events. */
export function StarsRepoPage({ org, owner, name, onRemoved }: { org: string; owner: string; name: string; onRemoved: () => void }) {
  const repos = useStarsRepos()
  const [reveal, setReveal] = useState<Reveal | null>(null)
  const wanted = `${owner}/${name}`.toLowerCase()
  const repo = repos.data?.data.find((item) => item.repo === wanted)
  const title = repo?.repo ?? wanted
  return (
    <PageContainer>
      <section className="flex min-w-0 flex-col gap-4" aria-labelledby="github-stars-title">
        <Link to="/app/$org/github-stars" params={{ org }} className="inline-flex min-h-11 w-fit items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
          <ChevronLeftIcon aria-hidden="true" className="size-4" />
          {en.githubStars.allRepos}
        </Link>
        <PageHeader titleId="github-stars-title" title={<span className="break-all">{title}</span>} description={en.githubStars.repoDescription} />
        {repos.isPending ? <Skeleton aria-label={en.githubStars.loading} className="h-40 w-full" /> : repos.isError ? (
          <ErrorState level="page" detail={errorText(repos.error)} onRetry={() => void repos.refetch()} />
        ) : !repo ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>{en.githubStars.repoNotFoundTitle}</EmptyTitle>
              <EmptyDescription>{en.githubStars.repoNotFoundBody(wanted)}</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <>
            {reveal ? <RevealPanel reveal={reveal} onDismiss={() => setReveal(null)} /> : null}
            <RepoPanel repo={repo} onReveal={setReveal} onRemoved={onRemoved} />
            <RepoView repoId={repo.id} />
          </>
        )}
      </section>
    </PageContainer>
  )
}
