import { useState, type FormEvent } from 'react'

import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { ApiProblem } from '@/lib/apiProblem'
import { obsLogs as t } from '@/messages/obsLogs'

import { useObsLogSearch } from './api'
import { LogRow } from './LogRow'

export type LogRange = keyof typeof t.ranges
const spans: Record<LogRange, number> = { '1h': 3_600_000, '24h': 86_400_000, '7d': 7 * 86_400_000 }
const floorMinute = (ms: number) => Math.floor(ms / 60_000) * 60_000

type Props = { org: string; site: string; siteId: number; timezone: string; initialQuery?: string; onQueryChange?: (q: string) => void }

/** Log search: a query box, a range, newest-first rows and a "Load more" button. */
export function LogSearch({ org, site, siteId, timezone, initialQuery = '', onQueryChange }: Props) {
  const [draft, setDraft] = useState(initialQuery)
  const [q, setQ] = useState(initialQuery)
  const [range, setRange] = useState<LogRange>('24h')
  const [window] = useState(() => floorMinute(Date.now()))
  const to = new Date(window + 60_000).toISOString()
  const from = new Date(window + 60_000 - spans[range]).toISOString()
  const logs = useObsLogSearch(siteId, q, from, to)

  function submit(event: FormEvent) {
    event.preventDefault()
    setQ(draft)
    onQueryChange?.(draft.trim())
  }

  const rows = logs.data?.pages.flatMap((page) => page.data) ?? []
  const problem = logs.error instanceof ApiProblem && logs.error.status === 400 ? logs.error : null

  return (
    <div className="flex flex-col gap-4">
      <form className="flex flex-col gap-3 sm:flex-row sm:items-end" onSubmit={submit} role="search">
        <Field className="flex-1">
          <FieldLabel htmlFor="log-query">{t.search}</FieldLabel>
          <Input id="log-query" className="min-h-11 font-mono sm:min-h-9" maxLength={300} placeholder={t.searchPlaceholder} value={draft} onChange={(event) => setDraft(event.target.value)} />
          {problem ? (
            <p role="alert" className="text-sm text-destructive">{t.syntaxError(problem.position ?? 0, problem.detail ?? problem.message)}</p>
          ) : <FieldDescription>{t.searchHelp}</FieldDescription>}
        </Field>
        <Field className="sm:w-44">
          <FieldLabel htmlFor="log-range">{t.range}</FieldLabel>
          <Select value={range} onValueChange={(value) => setRange(value as LogRange)}>
            <SelectTrigger className="min-h-11 w-full sm:min-h-9" id="log-range"><SelectValue /></SelectTrigger>
            <SelectContent><SelectGroup>
              {(Object.keys(spans) as LogRange[]).map((key) => <SelectItem key={key} value={key}>{t.ranges[key]}</SelectItem>)}
            </SelectGroup></SelectContent>
          </Select>
        </Field>
        <Button className="min-h-11 sm:min-h-9" type="submit">{t.search}</Button>
      </form>
      {logs.isPending ? <Skeleton className="h-40 w-full" aria-label={t.loading} /> : null}
      {logs.isError && !problem ? <ErrorState detail={logs.error.message} onRetry={() => void logs.refetch()} /> : null}
      {logs.isSuccess && rows.length === 0 ? (
        <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">{q.trim() ? t.emptyFiltered : t.emptyNone}</p>
      ) : null}
      {rows.length > 0 ? (
        <ul className="rounded-xl border bg-card" aria-label={t.title}>
          {rows.map((log, index) => <LogRow key={`${log.ts}-${index}`} log={log} org={org} site={site} timezone={timezone} />)}
        </ul>
      ) : null}
      {logs.hasNextPage ? (
        <div><Button variant="outline" disabled={logs.isFetchingNextPage} onClick={() => void logs.fetchNextPage()}>{logs.isFetchingNextPage ? t.loadingMore : t.loadMore}</Button></div>
      ) : null}
    </div>
  )
}
