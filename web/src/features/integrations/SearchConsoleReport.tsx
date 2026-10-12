import { useId, useMemo, useState } from 'react'
import { ArrowDownIcon, ArrowUpIcon, ChevronsUpDownIcon } from 'lucide-react'

import { ErrorState } from '@/components/ErrorState'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { cn } from '@/lib/utils'
import { fmt } from '@/lib/format'
import { en } from '@/messages/en'

import { useSearchConsoleTop, type SearchConsoleDimension, type SearchConsoleRow } from './api'

const t = en.integrationsUi

export type SortKey = 'clicks' | 'impressions' | 'ctr' | 'position'
export type SortState = { key: SortKey; direction: 'asc' | 'desc' }

const RANGE_DAYS = [7, 28, 90] as const

export function rangeDates(days: number, now = new Date()): { from: string; to: string } {
  const to = now.toISOString().slice(0, 10)
  const start = new Date(`${to}T00:00:00Z`)
  start.setUTCDate(start.getUTCDate() - (days - 1))
  return { from: start.toISOString().slice(0, 10), to }
}

export function sortRows(rows: SearchConsoleRow[], sort: SortState): SearchConsoleRow[] {
  const factor = sort.direction === 'asc' ? 1 : -1
  return [...rows].sort((a, b) => (a[sort.key] - b[sort.key]) * factor || a.key.localeCompare(b.key))
}

export function filterRows(rows: SearchConsoleRow[], text: string): SearchConsoleRow[] {
  const needle = text.trim().toLowerCase()
  return needle ? rows.filter((row) => row.key.toLowerCase().includes(needle)) : rows
}

const columns: Array<{ key: SortKey; label: string; format: (row: SearchConsoleRow) => string }> = [
  { key: 'clicks', label: t.clicks, format: (row) => fmt.number(row.clicks) },
  { key: 'impressions', label: t.impressions, format: (row) => fmt.number(row.impressions) },
  { key: 'ctr', label: t.ctr, format: (row) => fmt.percent(row.ctr) },
  { key: 'position', label: t.position, format: (row) => row.position.toFixed(1) },
]

export function SearchConsoleReport({ siteId, orgId }: { siteId: number; orgId: number }) {
  const [dimension, setDimension] = useState<SearchConsoleDimension>('query')
  const [days, setDays] = useState<number>(28)
  const [filter, setFilter] = useState('')
  const [sort, setSort] = useState<SortState>({ key: 'clicks', direction: 'desc' })
  const range = useMemo(() => rangeDates(days), [days])
  const report = useSearchConsoleTop(siteId, orgId, dimension, range.from, range.to)
  const ids = useId()
  const rows = useMemo(
    () => (report.data ? sortRows(filterRows(report.data.rows, filter), sort) : []),
    [report.data, filter, sort],
  )
  const name = dimension === 'query' ? t.tabQueries : t.tabPages

  function toggleSort(key: SortKey) {
    setSort((current) =>
      current.key === key
        ? { key, direction: current.direction === 'desc' ? 'asc' : 'desc' }
        : { key, direction: key === 'position' ? 'asc' : 'desc' },
    )
  }

  return (
    <section aria-labelledby={`${ids}-heading`} className="flex min-w-0 flex-col gap-4 rounded-xl border bg-card p-5 text-card-foreground">
      <header className="flex flex-col gap-1">
        <h2 className="font-semibold" id={`${ids}-heading`}>{t.reportHeading}</h2>
        <p className="text-sm text-muted-foreground">{t.reportIntro}</p>
      </header>

      <div className="flex flex-wrap items-end gap-3">
        <div aria-label={t.reportHeading} className="inline-flex rounded-lg border p-0.5" role="group">
          {([['query', t.tabQueries], ['page', t.tabPages]] as const).map(([value, label]) => (
            <button
              aria-pressed={dimension === value}
              className={cn(
                'min-h-11 rounded-md px-3 text-sm font-medium focus-visible:outline-2 focus-visible:outline-ring',
                dimension === value ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:text-foreground',
              )}
              key={value}
              onClick={() => setDimension(value)}
              type="button"
            >
              {label}
            </button>
          ))}
        </div>
        <label className="flex flex-col gap-1 text-sm font-medium" htmlFor={`${ids}-range`}>
          {t.range}
          <select
            className="min-h-11 rounded-lg border border-input bg-background px-2.5 text-sm font-normal"
            id={`${ids}-range`}
            onChange={(event) => setDays(Number(event.target.value))}
            value={days}
          >
            {RANGE_DAYS.map((value) => <option key={value} value={value}>{t.rangeOption(value)}</option>)}
          </select>
        </label>
        <label className="flex min-w-48 flex-1 flex-col gap-1 text-sm font-medium" htmlFor={`${ids}-filter`}>
          {t.search}
          <Input
            className="min-h-11"
            id={`${ids}-filter`}
            onChange={(event) => setFilter(event.target.value)}
            placeholder={t.searchPlaceholder}
            type="search"
            value={filter}
          />
        </label>
      </div>

      {report.isPending ? (
        <div aria-busy="true" className="flex flex-col gap-2" role="status">
          <span className="sr-only">{t.reportLoading}</span>
          {[0, 1, 2, 3].map((item) => <Skeleton className="h-9 w-full" key={item} />)}
        </div>
      ) : report.isError ? (
        <ErrorState detail={t.reportError} onRetry={() => void report.refetch()} />
      ) : report.data.rows.length === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>{name}</EmptyTitle>
            <EmptyDescription>{t.reportEmpty}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <>
          <p className="text-sm text-muted-foreground" role="status">
            {t.reportSummary(report.data.rows.length, fmt.number(report.data.totals.clicks), fmt.number(report.data.totals.impressions), report.data.from, report.data.to)}
          </p>
          {rows.length === 0 ? (
            <p className="text-sm text-muted-foreground" role="status">{t.reportNoMatch}</p>
          ) : (
            <div className="max-w-full overflow-x-auto">
              <Table>
                <caption className="sr-only">{t.tableCaption(name, report.data.from, report.data.to)}</caption>
                <TableHeader>
                  <TableRow>
                    <TableHead scope="col">{dimension === 'query' ? t.keyQuery : t.keyPage}</TableHead>
                    {columns.map((column) => {
                      const active = sort.key === column.key
                      const label = active ? (sort.direction === 'asc' ? t.ascending : t.descending) : ''
                      const Icon = !active ? ChevronsUpDownIcon : sort.direction === 'asc' ? ArrowUpIcon : ArrowDownIcon
                      return (
                        <TableHead
                          aria-sort={active ? (sort.direction === 'asc' ? 'ascending' : 'descending') : 'none'}
                          className="text-right"
                          key={column.key}
                          scope="col"
                        >
                          <button
                            aria-label={active ? t.sortBy(column.label, label) : undefined}
                            className="inline-flex min-h-11 items-center gap-1 px-1 font-medium focus-visible:outline-2 focus-visible:outline-ring"
                            onClick={() => toggleSort(column.key)}
                            type="button"
                          >
                            {column.label}
                            <Icon aria-hidden="true" className="size-3.5" />
                          </button>
                        </TableHead>
                      )
                    })}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.map((row) => (
                    <TableRow key={row.key}>
                      <TableCell className="max-w-[16rem] break-words whitespace-normal" title={row.key}>{row.key}</TableCell>
                      {columns.map((column) => (
                        <TableCell className="text-right tabular-nums" key={column.key}>{column.format(row)}</TableCell>
                      ))}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
          <p className="text-xs text-muted-foreground">{t.rowsShown(rows.length, report.data.rows.length)}</p>
        </>
      )}
    </section>
  )
}
