import { useCapability } from '@/lib/authorization'
import { EmptyState } from '@/components/EmptyState'
import { ErrorState } from '@/components/ErrorState'
import { Field, FieldLabel } from '@/components/ui/field'
import { Skeleton } from '@/components/ui/skeleton'
import type { Filter } from '@/lib/search'

import { useRetentionReport } from './api'
import { RetentionHeatmap } from './RetentionHeatmap'
import type { RetentionIdentity, RetentionInterval } from './types'

type RetentionPageProps = {
  siteId: number
  dateRange: { from: string; to: string }
  filters: Filter[]
  interval: RetentionInterval
  identity: RetentionIdentity
  profilesEnabled: boolean
  onIntervalChange: (interval: RetentionInterval) => void
  onIdentityChange: (identity: RetentionIdentity) => void
}

function RetentionContent({ siteId, dateRange, filters, interval, identity, profilesEnabled, onIntervalChange, onIdentityChange }: RetentionPageProps) {
  const report = useRetentionReport(siteId, dateRange, filters, interval, identity)

  return (
    <main className="flex min-w-0 flex-col gap-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <header>
          <h1 className="text-xl font-semibold tracking-tight">Retention</h1>
          <p className="text-sm text-muted-foreground">
            {identity === 'profile'
              ? 'See how many identified users return in later cohort periods.'
              : 'See how many daily identities return for another session on the same day.'}
          </p>
        </header>
        <div className="flex flex-wrap gap-3">
          {profilesEnabled ? (
            <Field className="w-44">
              <FieldLabel htmlFor="retention-identity">Identify visitors by</FieldLabel>
              <select
                className="h-9 rounded-md border bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
                id="retention-identity"
                value={identity}
                onChange={(event) => onIdentityChange(event.target.value as RetentionIdentity)}
              >
                <option value="anonymous">Anonymous day</option>
                <option value="profile">Identity profile</option>
              </select>
            </Field>
          ) : null}
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
          interval={interval}
          retentionMode={report.data.retention_mode}
        />
      )}
    </main>
  )
}

export function RetentionPage(props: RetentionPageProps) {
  const canReadProfiles = useCapability('profiles.read')
  return <RetentionContent {...props} profilesEnabled={props.profilesEnabled && canReadProfiles} identity={canReadProfiles ? props.identity : 'anonymous'} />
}
