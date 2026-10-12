import { useState } from 'react'

import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Field, FieldLabel } from '@/components/ui/field'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { AccessDenied, useCapability } from '@/lib/authorization'
import { fmt } from '@/lib/format'
import { en } from '@/messages/en'

import { usePowerUsers } from './api'
import { thresholdChoices, type PowerMetric } from './types'

const t = en.cohorts
const selectClass = 'h-11 w-full rounded-md border bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring md:h-9'

function formatDate(value: string): string {
  return value ? new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' }).format(new Date(value)) : '—'
}

function PowerUsersContent({
  siteId, dateRange, canEdit, onSaveAsCohort,
}: {
  siteId: number
  dateRange: { from: string; to: string }
  canEdit: boolean
  onSaveAsCohort: (metric: PowerMetric, min: number) => void
}) {
  const [metric, setMetric] = useState<PowerMetric>('sessions')
  const [min, setMin] = useState<number>(2)
  const users = usePowerUsers(siteId, dateRange, metric, min)
  const metricLabel = metric === 'events' ? t.metricEvents : t.metricSessions
  return (
    <section aria-labelledby="power-users-title" className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 className="text-base font-semibold" id="power-users-title">{t.powerTitle}</h2>
        <p className="text-sm text-muted-foreground">{t.powerDescription}</p>
      </div>
      <div className="grid gap-3 sm:max-w-lg sm:grid-cols-2">
        <Field>
          <FieldLabel htmlFor="power-metric">{t.powerMetric}</FieldLabel>
          <select className={selectClass} id="power-metric" value={metric} onChange={(event) => setMetric(event.target.value as PowerMetric)}>
            <option value="sessions">{t.metricSessions}</option>
            <option value="events">{t.metricEvents}</option>
          </select>
        </Field>
        <Field>
          <FieldLabel htmlFor="power-threshold">{t.powerThreshold}</FieldLabel>
          <select className={selectClass} id="power-threshold" value={min} onChange={(event) => setMin(Number(event.target.value))}>
            {thresholdChoices.map((choice) => <option key={choice} value={choice}>{t.powerThresholdOption(choice, metricLabel)}</option>)}
          </select>
        </Field>
      </div>
      {users.isError ? (
        <ErrorState detail={users.error.message} onRetry={() => void users.refetch()} />
      ) : users.isPending ? (
        <div aria-label="Loading power users" className="flex flex-col gap-3">
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-32 w-full" />
        </div>
      ) : (
        <>
          <p aria-live="polite" className="text-sm text-muted-foreground" role="status">
            {t.powerQualifying(`${fmt.number(users.data.qualifying)}${users.data.qualifying_capped ? '+' : ''}`)}
          </p>
          {users.data.data.length === 0 ? (
            <Empty>
              <EmptyHeader>
                <EmptyTitle>{t.powerEmpty}</EmptyTitle>
              </EmptyHeader>
            </Empty>
          ) : (
            <div className="min-w-0 overflow-x-auto rounded-xl border bg-card">
              <Table aria-label={t.powerTitle}>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t.powerRank}</TableHead>
                    <TableHead>{t.colExternalId}</TableHead>
                    <TableHead className="text-right">{t.colSessions}</TableHead>
                    <TableHead className="text-right">{t.colEvents}</TableHead>
                    <TableHead>{t.colLastSeen}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {users.data.data.map((user, index) => (
                    <TableRow key={user.id}>
                      <TableCell className="tabular-nums">{index + 1}</TableCell>
                      <TableCell className="font-medium">{user.external_id}</TableCell>
                      <TableCell className="text-right tabular-nums">{fmt.number(user.sessions)}</TableCell>
                      <TableCell className="text-right tabular-nums">{fmt.number(user.events)}</TableCell>
                      <TableCell>{formatDate(user.last_seen_at)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
          {canEdit ? (
            <div>
              <Button className="h-11 md:h-8" type="button" variant="outline" onClick={() => onSaveAsCohort(metric, min)}>{t.powerSaveAsCohort}</Button>
            </div>
          ) : null}
        </>
      )}
    </section>
  )
}

// Person-level lists need the profiles permission: external ids can be personal data.
export function PowerUsersPanel(props: Parameters<typeof PowerUsersContent>[0]) {
  const allowed = useCapability('profiles.read')
  if (!allowed) return <AccessDenied subject={t.powerNeedsPermission} />
  return <PowerUsersContent {...props} />
}
