import { EmptyState } from '@/components/EmptyState'
import { ErrorState } from '@/components/ErrorState'
import { Field, FieldLabel } from '@/components/ui/field'
import { Skeleton } from '@/components/ui/skeleton'
import type { Filter } from '@/lib/search'

import { useRetentionReport } from './api'
import { RetentionHeatmap } from './RetentionHeatmap'
import type { RetentionInterval } from './types'

type RetentionPageProps = {
  siteId: number
  dateRange: { from: string; to: string }
  filters: Filter[]
  interval: RetentionInterval
  onIntervalChange: (interval: RetentionInterval) => void
}

export function RetentionPage({ siteId, dateRange, filters, interval, onIntervalChange }: RetentionPageProps) {
  const report = useRetentionReport(siteId, dateRange, filters, interval)

  return (
    <main className="flex min-w-0 flex-col gap-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <header>
          <h1 className="text-xl font-semibold tracking-tight">Retention</h1>
          <p className="text-sm text-muted-foreground">See how many daily identities return for another session on the same day.</p>
        </header>
        <Field className="w-44">
          <FieldLabel htmlFor="retention-interval">Group cohorts by</FieldLabel>
          <select
            className="h-9 rounded-md border bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
            id="retention-interval"
            value={interval}
            onChange={(event) => onIntervalChange(event.target.value as RetentionInterval)}
          >
            <option value="day">Day</option>
            <option value="week">Week</option>
            <option value="month">Month</option>
          </select>
        </Field>
      </div>
      {report.isError ? (
        <ErrorState detail={report.error?.detail} onRetry={() => void report.refetch()} />
      ) : report.isPending || report.isLoading ? (
        <div aria-label="Loading retention" className="flex flex-col gap-3 rounded-xl border p-4">
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-4/5" />
          <Skeleton className="h-10 w-3/5" />
        </div>
      ) : !report.data || report.data.cohorts.length === 0 ? (
        <EmptyState />
      ) : (
        <RetentionHeatmap
          cohorts={report.data.cohorts}
          identityWindow={report.data.identity_window}
          retentionMode={report.data.retention_mode}
        />
      )}
    </main>
  )
}
