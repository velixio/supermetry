import { useEffect, useState, type FormEvent } from 'react'
import { useMutation, useQueries, useQuery } from '@tanstack/react-query'

import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { fmt } from '@/lib/format'
import { en } from '@/messages/en'
import { PoweredBySupermetry } from '@/components/PoweredBySupermetry'
import { BrandLogo } from '@/components/BrandLogo'

import {
  loadShareSummary,
  loadShareSection,
  openSharedDashboard,
  ShareProblem,
  type ShareQueryResult,
  type ShareSection,
  type ShareSummary,
} from './api'

export { ShareProblem, type ShareSummary }

type SharedDashboardProps = {
  slug: string
  loadSummary?: (slug: string) => Promise<ShareSummary>
  openShare?: (slug: string, password: string) => Promise<void>
  loadSection?: (slug: string, section: ShareSection) => Promise<ShareQueryResult>
}

const sectionLabels: Record<Exclude<ShareSection, 'kpis'>, string> = {
  chart: 'Traffic trend',
  pages: 'Pages',
  sources: 'Sources',
  locations: 'Locations',
  devices: 'Devices',
  where: 'Where',
  weekly_rhythm: 'Weekly rhythm',
  goals: 'Goals',
}

function displayValue(value: unknown): string {
  if (typeof value === 'number') return fmt.number(value)
  if (typeof value === 'string') return value || 'Unknown'
  return '—'
}

function numericValue(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0
}

function TrafficChart({ result }: { result: ShareQueryResult }) {
  const rows = result.rows.slice(0, 8)
  const maximum = Math.max(1, ...rows.flatMap((row) => [numericValue(row.visitors), numericValue(row.pageviews)]))
  return (
    <figure className="mt-3" aria-label="Visitors and pageviews over time">
      <div className="mb-3 flex gap-4 text-xs text-muted-foreground" aria-hidden="true">
        <span className="flex items-center gap-1.5"><span className="size-2 rounded-full bg-chart-1" />Visitors</span>
        <span className="flex items-center gap-1.5"><span className="size-2 rounded-full bg-chart-2" />Pageviews</span>
      </div>
      <div className="grid gap-2" aria-hidden="true">
        {rows.map((row, index) => (
          <div className="grid grid-cols-[6rem_1fr] items-center gap-2" key={index}>
            <span className="truncate text-xs text-muted-foreground">{displayValue(row.time)}</span>
            <div className="grid gap-1">
              <span className="h-2 min-w-px rounded-sm bg-chart-1" style={{ width: `${numericValue(row.visitors) / maximum * 100}%` }} />
              <span className="h-2 min-w-px rounded-sm bg-chart-2" style={{ width: `${numericValue(row.pageviews) / maximum * 100}%` }} />
            </div>
          </div>
        ))}
      </div>
      <table className="sr-only">
        <caption>Traffic trend values</caption>
        <thead><tr><th>Time</th><th>Visitors</th><th>Pageviews</th></tr></thead>
        <tbody>{rows.map((row, index) => <tr key={index}><td>{displayValue(row.time)}</td><td>{displayValue(row.visitors)}</td><td>{displayValue(row.pageviews)}</td></tr>)}</tbody>
      </table>
    </figure>
  )
}

function SectionPanel({ section, result, pending }: { section: Exclude<ShareSection, 'kpis'>; result?: ShareQueryResult; pending: boolean }) {
  const columns = result?.columns ?? []
  return (
    <section className="min-w-0 rounded-lg border bg-card p-4" aria-busy={pending}>
      <h2 className="text-base font-semibold">{sectionLabels[section]}</h2>
      {pending ? <Skeleton className="mt-4 h-28 w-full" /> : result?.rows.length && section === 'chart' ? (
        <TrafficChart result={result} />
      ) : result?.rows.length ? (
        <div className="mt-3 overflow-x-auto">
          <table className="w-full text-left text-sm">
            <caption className="sr-only">{sectionLabels[section]} data</caption>
            <thead><tr>{columns.map((column) => <th className="pb-2 pr-4 font-medium text-muted-foreground" key={column}>{column.replaceAll('_', ' ')}</th>)}</tr></thead>
            <tbody>{result.rows.slice(0, 8).map((row, index) => <tr className="border-t" key={index}>{columns.map((column) => <td className="py-2 pr-4 tabular-nums" key={column}>{displayValue(row[column])}</td>)}</tr>)}</tbody>
          </table>
        </div>
      ) : <p className="mt-3 text-sm text-muted-foreground">No data in this range.</p>}
    </section>
  )
}

const metrics = [
  ['visitors', 'Visitors'],
  ['visits', 'Visits'],
  ['pageviews', 'Pageviews'],
  ['views_per_visit', 'Views per visit'],
  ['bounce_rate', 'Bounce rate'],
  ['visit_duration', 'Visit duration'],
  ['engagement_time', 'Engagement time'],
] as const

function metricValue(metric: string, value: number): string {
  if (metric === 'bounce_rate') return fmt.percent(value)
  if (metric === 'visit_duration' || metric === 'engagement_time') {
    return fmt.duration(value)
  }
  return fmt.number(value)
}

function PasswordPrompt({
  slug,
  detail,
  openShare,
  onOpened,
}: {
  slug: string
  detail: string
  openShare: (slug: string, password: string) => Promise<void>
  onOpened: () => Promise<unknown>
}) {
  const [password, setPassword] = useState('')
  const mutation = useMutation({
    mutationFn: () => openShare(slug, password),
    onSuccess: onOpened,
  })
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    mutation.mutate()
  }

  return (
    <main className="grid min-h-dvh place-items-center px-6">
      <form className="w-full max-w-sm border-l-2 border-accent pl-5" onSubmit={submit}>
        <FieldGroup>
          <div className="flex flex-col gap-1">
            <h1 className="text-2xl font-semibold tracking-tight">Shared dashboard</h1>
            <p className="text-sm text-muted-foreground">{detail}</p>
          </div>
          <Field data-invalid={mutation.isError || undefined}>
            <FieldLabel htmlFor="share-password">Password</FieldLabel>
            <Input
              id="share-password"
              type="password"
              autoComplete="current-password"
              value={password}
              aria-invalid={mutation.isError || undefined}
              onChange={(event) => setPassword(event.target.value)}
            />
            <FieldDescription>This link is protected by its owner.</FieldDescription>
            <FieldError>
              {mutation.isError
                ? mutation.error instanceof Error
                  ? mutation.error.message
                  : 'The password could not be checked.'
                : null}
            </FieldError>
          </Field>
          <Button type="submit" disabled={mutation.isPending || password.length === 0}>
            {mutation.isPending ? 'Opening…' : 'Open dashboard'}
          </Button>
        </FieldGroup>
      </form>
    </main>
  )
}

export function SharedDashboard({
  slug,
  loadSummary = loadShareSummary,
  openShare = openSharedDashboard,
  loadSection = loadShareSection,
}: SharedDashboardProps) {
  const summary = useQuery({
    queryKey: ['share', slug, 'summary', '7d'],
    queryFn: () => loadSummary(slug),
    retry: false,
  })
  const enabledSections = (summary.data?.sections ?? []).filter((section): section is Exclude<ShareSection, 'kpis'> => section !== 'kpis')
  const sectionQueries = useQueries({
    queries: enabledSections.map((section) => ({
      queryKey: ['share', slug, 'section', section],
      queryFn: () => loadSection(slug, section),
      retry: false,
    })),
  })

  useEffect(() => {
    const root = document.documentElement
    const wasDark = root.classList.contains('dark')
    const preferred = window.matchMedia?.('(prefers-color-scheme: dark)')
    const applyTheme = () => {
      if (summary.data?.theme === 'dark') root.classList.add('dark')
      if (summary.data?.theme === 'light') root.classList.remove('dark')
      if (summary.data?.theme === 'system') root.classList.toggle('dark', preferred?.matches ?? wasDark)
    }
    applyTheme()
    preferred?.addEventListener('change', applyTheme)
    return () => {
      preferred?.removeEventListener('change', applyTheme)
      root.classList.toggle('dark', wasDark)
    }
  }, [summary.data?.theme])

  if (summary.isError) {
    if (
      summary.error instanceof ShareProblem &&
      summary.error.type === 'share-password-required'
    ) {
      return (
        <PasswordPrompt
          slug={slug}
          detail={summary.error.message}
          openShare={openShare}
          onOpened={() => summary.refetch()}
        />
      )
    }
    return (
      <main className="grid min-h-dvh place-items-center px-6">
        <div className="max-w-md border-l-2 border-destructive pl-5">
          <h1 className="text-2xl font-semibold tracking-tight">Link unavailable</h1>
          <p role="alert" className="mt-2 text-sm text-muted-foreground">
            This shared dashboard is unavailable.
          </p>
        </div>
      </main>
    )
  }

  return (
    <main className={`mx-auto flex min-h-dvh w-full max-w-6xl flex-col gap-8 px-4 py-8 sm:px-6 ${summary.data?.transparent_background ? 'bg-transparent' : 'bg-background'}`}>
      <header className="flex items-end justify-between gap-4 border-b pb-5">
        <div className="flex flex-col gap-1 border-l-2 border-accent pl-4">
          <BrandLogo className="h-6 w-auto" />
          <h1 className="text-2xl font-semibold tracking-tight">Shared dashboard</h1>
        </div>
        <p className="text-sm text-muted-foreground">{summary.data?.date_label ?? 'Loading range…'}</p>
      </header>
      {summary.data?.sections.includes('kpis') || summary.isPending ? <section
        aria-busy={summary.isPending}
        aria-label="Analytics summary"
        className="grid grid-cols-2 border-y sm:grid-cols-4 lg:grid-cols-7"
      >
        {metrics.map(([metric, label]) => (
          <div key={metric} className="flex min-w-0 flex-col gap-1 border-b px-3 py-4 sm:border-r lg:border-b-0">
            <span className="truncate text-xs text-muted-foreground">{label}</span>
            {summary.isPending ? (
              <Skeleton className="h-7 w-16" />
            ) : (
              <span className="text-lg font-semibold tabular-nums">
                {metricValue(metric, summary.data?.data.totals[metric] ?? 0)}
              </span>
            )}
          </div>
        ))}
      </section> : null}
      {enabledSections.length ? (
        <div className="grid min-w-0 gap-4 md:grid-cols-2">
          {enabledSections.map((section, index) => (
            <SectionPanel key={section} section={section} result={sectionQueries[index]?.data} pending={sectionQueries[index]?.isPending ?? true} />
          ))}
        </div>
      ) : null}
      <footer className="text-xs text-muted-foreground">
        <PoweredBySupermetry className="hover:text-foreground" />
      </footer>
    </main>
  )
}
