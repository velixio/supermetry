import { useState, type FormEvent } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'

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
  openSharedDashboard,
  ShareProblem,
  type ShareSummary,
} from './api'

export { ShareProblem, type ShareSummary }

type SharedDashboardProps = {
  slug: string
  loadSummary?: (slug: string) => Promise<ShareSummary>
  openShare?: (slug: string, password: string) => Promise<void>
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
}: SharedDashboardProps) {
  const summary = useQuery({
    queryKey: ['share', slug, 'summary', '7d'],
    queryFn: () => loadSummary(slug),
    retry: false,
  })

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
          <h1 className="text-2xl font-semibold tracking-tight">Shared dashboard</h1>
          <p role="alert" className="mt-2 text-sm text-muted-foreground">
            {summary.error instanceof Error
              ? summary.error.message
              : 'The shared dashboard could not be opened.'}
          </p>
        </div>
      </main>
    )
  }

  return (
    <main className="mx-auto flex min-h-dvh w-full max-w-6xl flex-col gap-8 px-4 py-8 sm:px-6">
      <header className="flex items-end justify-between gap-4 border-b pb-5">
        <div className="flex flex-col gap-1 border-l-2 border-accent pl-4">
          <BrandLogo className="h-6 w-auto" />
          <h1 className="text-2xl font-semibold tracking-tight">Shared dashboard</h1>
        </div>
        <p className="text-sm text-muted-foreground">Last 7 days</p>
      </header>
      <section
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
                {metricValue(metric, summary.data?.totals[metric] ?? 0)}
              </span>
            )}
          </div>
        ))}
      </section>
      <footer className="text-xs text-muted-foreground">
        <PoweredBySupermetry className="hover:text-foreground" />
      </footer>
    </main>
  )
}
