import { useState } from 'react'

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { ErrorState } from '@/components/ErrorState'
import { fmt } from '@/lib/format'
import { en } from '@/messages/en'
import { AdminPage, formatDate } from './AdminCommon'
import { nativeSelectClass } from '@/lib/nativeSelect'
import { Button } from '@/components/ui/button'
import {
  useStarsAnnotations, useStarsRepos, useStarsSeries, useStarsSummary,
  type StarsGranularity, type StarsSummary,
} from './githubStarsApi'
import { AddRepoDialog, RepoPanel, RevealPanel, type Reveal } from './StarsRepos'
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

export function AdminGitHubStarsPage() {
  const repos = useStarsRepos()
  const [selected, setSelected] = useState<number | null>(null)
  const [adding, setAdding] = useState(false)
  const [reveal, setReveal] = useState<Reveal | null>(null)
  const list = repos.data?.data ?? []
  const current = list.find((repo) => repo.id === selected) ?? list[0]
  return (
    <AdminPage title={en.githubStars.title} description={en.githubStars.description}>
      {repos.isPending ? <Skeleton aria-label={en.githubStars.loading} className="h-40 w-full" /> : repos.isError ? (
        <ErrorState level="page" detail={errorText(repos.error)} onRetry={() => void repos.refetch()} />
      ) : !current ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>{en.githubStars.addFirstTitle}</EmptyTitle>
            <EmptyDescription>{en.githubStars.addFirstBody}</EmptyDescription>
          </EmptyHeader>
          <Button type="button" className="min-h-11" onClick={() => setAdding(true)}>{en.githubStars.addRepo}</Button>
        </Empty>
      ) : (
        <>
          <div className="flex flex-wrap items-end gap-2">
            <label className="flex min-w-0 flex-1 basis-56 flex-col gap-1 text-sm font-medium">
              {en.githubStars.repoSelector}
              <select className={`${nativeSelectClass} min-h-11`} value={current.id} onChange={(event) => setSelected(Number(event.target.value))}>
                {list.map((repo) => <option key={repo.id} value={repo.id}>{repo.repo}{repo.enabled ? '' : ` (${en.githubStars.disabled})`}</option>)}
              </select>
            </label>
            <Button type="button" variant="outline" className="min-h-11" onClick={() => setAdding(true)}>{en.githubStars.addRepo}</Button>
          </div>
          {reveal ? <RevealPanel reveal={reveal} onDismiss={() => setReveal(null)} /> : null}
          <RepoPanel key={current.id} repo={current} onReveal={setReveal} />
          <RepoView key={current.id} repoId={current.id} />
        </>
      )}
      <AddRepoDialog open={adding} onOpenChange={setAdding} onCreated={(created, id) => { setReveal(created); setSelected(id) }} />
    </AdminPage>
  )
}
