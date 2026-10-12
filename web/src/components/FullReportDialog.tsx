import { useId, useMemo, useState, type RefObject } from 'react'
import { ArrowDownIcon, ArrowUpIcon, DownloadIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { api } from '@/lib/api'
import { useCapability } from '@/lib/authorization'
import { formatBreakdownMetric, metricLabel } from '@/lib/breakdown-metrics'
import { countryLabel } from '@/lib/country'
import { downloadStatsCSV, queryBody } from '@/lib/csv'
import { useStats, type StatsQuery } from '@/lib/stats'
import { fullReport as t } from '@/messages/fullReport'

import { EmptyState } from './EmptyState'
import { ErrorState } from './ErrorState'
import { downloadStatsJSON } from './fullReportExport'

type Row = Record<string, unknown>

export type FullReportDialogProps = {
  dimension: string
  query: StatsQuery
  title: string
  allowSearch?: boolean
  onClose: () => void
  returnFocusRef?: RefObject<HTMLElement | null>
}

function label(dimension: string, row: Row): string {
  const raw = String(row[dimension] ?? '')
  if (raw === '') return 'Unknown'
  return dimension === 'country' ? countryLabel(raw) : raw
}

export function FullReportDialog({ dimension, query, title, allowSearch = true, onClose, returnFocusRef }: FullReportDialogProps) {
  const canExport = useCapability('data.export')
  const noteId = useId()
  const metrics = query.metrics.filter((metric) => metric !== 'pageviews')
  const sortMetric = query.metrics[0] ?? 'visitors'
  const [search, setSearch] = useState('')
  const [sort, setSort] = useState<{ column: string; desc: boolean }>({ column: sortMetric, desc: true })
  const [extra, setExtra] = useState<Row[]>([])
  const [cursor, setCursor] = useState<string | null | undefined>(undefined)
  const [loadingMore, setLoadingMore] = useState(false)
  const [chosen, setChosen] = useState<string[]>([dimension, ...metrics])
  const [busy, setBusy] = useState<'csv' | 'json' | null>(null)
  const [status, setStatus] = useState<{ ok: boolean; text: string }>()

  const reportQuery = useMemo<StatsQuery>(() => ({
    ...query,
    dimensions: dimension === 'region' || dimension === 'city' ? [dimension, 'country'] : [dimension],
    orderBy: [{ column: sortMetric, direction: 'desc' }],
    limit: 1000,
    cursor: undefined,
  }), [dimension, query, sortMetric])
  const result = useStats(reportQuery)
  const nextCursor = cursor === undefined ? result.data?.next_cursor ?? null : cursor
  const loaded = useMemo<Row[]>(() => [...((result.data?.rows ?? []) as Row[]), ...extra], [result.data?.rows, extra])

  const rows = useMemo(() => {
    const needle = allowSearch ? search.trim().toLowerCase() : ''
    const shown = needle ? loaded.filter((row) => label(dimension, row).toLowerCase().includes(needle) || String(row[dimension] ?? '').toLowerCase().includes(needle)) : loaded
    const direction = sort.desc ? -1 : 1
    return [...shown].sort((a, b) => {
      if (sort.column === dimension) return direction * label(dimension, a).localeCompare(label(dimension, b))
      return direction * (Number(a[sort.column] ?? 0) - Number(b[sort.column] ?? 0))
    })
  }, [allowSearch, dimension, loaded, search, sort])

  const loadMore = async () => {
    if (!nextCursor) return
    setLoadingMore(true)
    try {
      const { data, error } = await api.POST('/api/private/query', { body: queryBody({ ...reportQuery, cursor: nextCursor }) })
      if (error || !data || typeof data === 'string') throw error ?? new Error('empty response')
      setExtra((previous) => [...previous, ...((data.rows ?? []) as Row[])])
      setCursor(data.next_cursor ?? null)
    } catch {
      setStatus({ ok: false, text: t.exportFailed })
    } finally {
      setLoadingMore(false)
    }
  }

  const exportColumns = chosen.filter((column) => column === dimension || metrics.includes(column as never))
  const exportMetrics = exportColumns.filter((column) => column !== dimension)
  const run = async (kind: 'csv' | 'json') => {
    if (!canExport) return
    if (exportColumns.length === 0) {
      setStatus({ ok: false, text: t.needOneColumn })
      return
    }
    setBusy(kind)
    setStatus(undefined)
    try {
      const exportQuery = { ...reportQuery, metrics: (exportMetrics.length > 0 ? exportMetrics : [sortMetric]) as StatsQuery['metrics'], limit: undefined }
      if (kind === 'csv') {
        await downloadStatsCSV(exportQuery)
        setStatus({ ok: true, text: t.exported('CSV') })
      } else {
        const name = await downloadStatsJSON(exportQuery, dimension, exportColumns)
        setStatus({ ok: true, text: t.exported(name) })
      }
    } catch {
      setStatus({ ok: false, text: t.exportFailed })
    } finally {
      setBusy(null)
    }
  }

  const columns = [dimension, ...metrics]
  const toggle = (column: string, on: boolean) =>
    setChosen((previous) => (on ? [...previous, column] : previous.filter((candidate) => candidate !== column)))

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent
        className="flex max-h-[calc(100dvh-1rem)] flex-col overflow-hidden sm:max-w-4xl"
        onCloseAutoFocus={(event) => {
          if (returnFocusRef?.current) {
            event.preventDefault()
            returnFocusRef.current.focus()
          }
        }}
      >
        <DialogHeader>
          <DialogTitle>{t.title(title)}</DialogTitle>
          <DialogDescription>{allowSearch ? t.description : t.descriptionNoSearch}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <Input
            aria-label={t.searchLabel(dimension)}
            className="sm:max-w-xs"
            disabled={!allowSearch}
            placeholder={t.searchPlaceholder}
            type="search"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
          <p aria-live="polite" className="text-xs text-muted-foreground" role="status">
            {result.data ? (nextCursor ? t.summaryMore(loaded.length) : t.summary(rows.length, loaded.length)) : ''}
          </p>
        </div>
        <div className="min-h-0 flex-1 overflow-auto rounded-lg border" role="region" aria-label={t.tableLabel(title)} tabIndex={0}>
          {result.isError ? (
            <ErrorState detail={result.error?.detail} onRetry={() => void result.refetch()} />
          ) : result.isPending ? (
            <p className="py-12 text-center text-sm text-muted-foreground">{t.loading}</p>
          ) : rows.length === 0 ? (
            loaded.length === 0 ? <EmptyState /> : <p className="py-12 text-center text-sm text-muted-foreground">{t.empty}</p>
          ) : (
            <table aria-label={t.tableLabel(title)} className="w-full text-sm">
              <thead className="sticky top-0 bg-muted text-left">
                <tr>
                  {columns.map((column) => (
                    <th
                      key={column}
                      aria-sort={sort.column === column ? (sort.desc ? 'descending' : 'ascending') : 'none'}
                      className={column === dimension ? 'px-3 py-2 font-medium' : 'px-3 py-2 text-right font-medium'}
                      scope="col"
                    >
                      <button
                        className="inline-flex items-center gap-1 rounded outline-none focus-visible:ring-2 focus-visible:ring-ring"
                        type="button"
                        onClick={() => setSort((current) => ({ column, desc: current.column === column ? !current.desc : column !== dimension }))}
                      >
                        {metricLabel(column)}
                        {sort.column === column ? (sort.desc ? <ArrowDownIcon aria-label={t.sortedDesc} /> : <ArrowUpIcon aria-label={t.sortedAsc} />) : null}
                      </button>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map((row, index) => (
                  <tr key={`${String(row[dimension])}:${String(row.country ?? '')}:${index}`} className="border-t">
                    <td className="max-w-[16rem] px-3 py-2"><span className="block truncate" title={label(dimension, row)}>{label(dimension, row)}</span></td>
                    {metrics.map((metric) => (
                      <td key={metric} className="px-3 py-2 text-right tabular-nums">{formatBreakdownMetric(metric, row)}</td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
        {nextCursor ? (
          <Button className="self-start" disabled={loadingMore} size="sm" type="button" variant="outline" onClick={() => void loadMore()}>
            {loadingMore ? t.loadingMore : t.loadMore}
          </Button>
        ) : null}
        <fieldset aria-describedby={noteId} className="flex flex-col gap-2 border-t pt-3" disabled={!canExport}>
          <legend className="sr-only">{t.exportTitle}</legend>
          <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
            <span className="text-xs font-medium">{t.columnsLegend}</span>
            {columns.map((column) => (
              <label key={column} className="flex items-center gap-2 text-sm">
                <Checkbox checked={chosen.includes(column)} onCheckedChange={(on) => toggle(column, on === true)} />
                {metricLabel(column)}
              </label>
            ))}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button disabled={busy !== null || !canExport} size="sm" type="button" variant="outline" onClick={() => void run('csv')}>
              <DownloadIcon data-icon="inline-start" />
              {busy === 'csv' ? t.exporting : t.csv}
            </Button>
            <Button disabled={busy !== null || !canExport} size="sm" type="button" variant="outline" onClick={() => void run('json')}>
              <DownloadIcon data-icon="inline-start" />
              {busy === 'json' ? t.exporting : t.json}
            </Button>
          </div>
          <p className="text-xs text-muted-foreground" id={noteId}>{canExport ? t.exportScope : t.noPermission}</p>
          {status ? <p className={status.ok ? 'text-xs text-muted-foreground' : 'text-xs text-destructive'} role={status.ok ? 'status' : 'alert'}>{status.text}</p> : null}
        </fieldset>
      </DialogContent>
    </Dialog>
  )
}
