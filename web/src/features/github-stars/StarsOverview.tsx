import { Link } from '@tanstack/react-router'
import { useId, useState } from 'react'
import { Line, LineChart, ResponsiveContainer, YAxis } from 'recharts'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { formatDate } from '@/features/admin/AdminCommon'
import { fmt } from '@/lib/format'
import { en } from '@/messages/en'
import { useStarsRepos, useStarsSeries, useStarsSummary, type StarsPoint, type StarsRepo } from './githubStarsApi'
import { AddRepoDialog, RevealPanel, type Reveal } from './StarsRepos'

const t = en.githubStars
const maxErrorChars = 80

function signed(value: number) {
  return `${value > 0 ? '+' : ''}${fmt.full(value)}`
}

/** Stars over the last 30 days as a small static line: no axes, no animation. */
function Sparkline({ points }: { points: StarsPoint[] }) {
  if (points.length === 0) return <p className="h-10 text-xs text-muted-foreground">{t.sparkNone}</p>
  const first = points[0].cumulative
  const last = points[points.length - 1].cumulative
  return (
    <div role="img" aria-label={t.sparkLabel(fmt.full(first), fmt.full(last))} className="h-10 w-full min-w-0">
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={points} margin={{ top: 4, right: 2, bottom: 4, left: 2 }}>
          <YAxis hide domain={['dataMin', 'dataMax']} />
          <Line dataKey="cumulative" type="monotone" stroke="var(--chart-1)" strokeWidth={2} dot={false} isAnimationActive={false} />
        </LineChart>
      </ResponsiveContainer>
    </div>
  )
}

function statusLine(repo: StarsRepo): string {
  if (!repo.enabled) return t.statusDisabled
  if (repo.last_error) return repo.last_error.length > maxErrorChars ? `${repo.last_error.slice(0, maxErrorChars)}…` : repo.last_error
  return repo.last_reconcile_at ? t.statusSynced(formatDate(repo.last_reconcile_at)) : t.statusNever
}

/** One repository: a single link to its page. Each card loads and fails on its own. */
export function RepoCard({ org, repo }: { org: string; repo: StarsRepo }) {
  const summary = useStarsSummary(repo.id)
  const series = useStarsSeries(repo.id, 'day')
  const [owner, name] = repo.repo.split('/')
  const stars = summary.data ? fmt.full(summary.data.stars) : undefined
  const failed = summary.isError
  return (
    <Link
      to="/app/$org/github-stars/$owner/$name"
      params={{ org, owner, name }}
      aria-label={stars ? t.cardLabel(repo.repo, stars) : repo.repo}
      className="flex min-h-11 min-w-0 flex-col gap-2 rounded-xl border bg-card p-4 outline-none transition-colors hover:bg-muted/50 focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      <span className="truncate text-sm font-medium" title={repo.repo}>{repo.repo}</span>
      {summary.isPending ? <Skeleton aria-hidden="true" className="h-9 w-32" /> : failed ? (
        <span className="text-sm text-destructive">{t.cardError}</span>
      ) : (
        <>
          <span className="text-3xl font-semibold tabular-nums">{stars}</span>
          <span className="text-sm tabular-nums text-muted-foreground">{t.weekChange(signed(summary.data!.net_7d))}</span>
        </>
      )}
      {series.isPending ? <Skeleton aria-hidden="true" className="h-10 w-full" /> : series.isError ? null : <Sparkline points={series.data.data} />}
      <span className="truncate text-xs text-muted-foreground" title={repo.last_error ?? undefined}>{statusLine(repo)}</span>
    </Link>
  )
}

export function StarsOverview({ org }: { org: string }) {
  const repos = useStarsRepos()
  const headingId = useId()
  const [adding, setAdding] = useState(false)
  const [reveal, setReveal] = useState<Reveal | null>(null)
  const list = repos.data?.data ?? []
  const add = <Button type="button" className="min-h-11" onClick={() => setAdding(true)}>{t.addRepo}</Button>
  return (
    <PageContainer>
      <section className="flex min-w-0 flex-col gap-4" aria-labelledby={headingId}>
        <PageHeader titleId={headingId} title={t.title} description={t.description} actions={list.length > 0 ? add : undefined} />
        {reveal ? <RevealPanel reveal={reveal} onDismiss={() => setReveal(null)} /> : null}
        {repos.isPending ? <Skeleton aria-label={t.loading} className="h-40 w-full" /> : repos.isError ? (
          <ErrorState level="page" detail={repos.error instanceof Error ? repos.error.message : undefined} onRetry={() => void repos.refetch()} />
        ) : list.length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>{t.addFirstTitle}</EmptyTitle>
              <EmptyDescription>{t.addFirstBody}</EmptyDescription>
            </EmptyHeader>
            {add}
          </Empty>
        ) : (
          <ul aria-label={t.repoList} className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {list.map((repo) => <li key={repo.id} className="min-w-0"><RepoCard org={org} repo={repo} /></li>)}
          </ul>
        )}
        <AddRepoDialog open={adding} onOpenChange={setAdding} onCreated={(created) => setReveal(created)} />
      </section>
    </PageContainer>
  )
}
