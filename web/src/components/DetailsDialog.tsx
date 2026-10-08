import { useMemo, useState } from 'react'
import {
  flexRender,
  getCoreRowModel,
  useReactTable,
  type ColumnDef,
  type SortingState,
} from '@tanstack/react-table'
import { ArrowDownIcon, ArrowUpIcon, DownloadIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { countryLabel } from '@/lib/country'
import { downloadStatsCSV } from '@/lib/csv'
import { fmt } from '@/lib/format'
import { useStats, type StatsQuery } from '@/lib/stats'

import { EmptyState } from './EmptyState'
import { ErrorState } from './ErrorState'
import { Flag } from './Flag'

type DetailRow = Record<string, unknown>

type DetailsDialogProps = {
  dimension: string
  query: StatsQuery
  title?: string
}

function DetailsContent({ dimension, query, title }: DetailsDialogProps) {
  const metric = query.metrics[0] ?? 'visitors'
  const [search, setSearch] = useState('')
  const [cursor, setCursor] = useState<string>()
  const [cursorHistory, setCursorHistory] = useState<string[]>([])
  const [sorting, setSorting] = useState<SortingState>([
    { id: metric, desc: true },
  ])
  const [exportError, setExportError] = useState<string>()
  const [exporting, setExporting] = useState(false)
  const searchValue = search.trim()
  const sort = sorting[0]
  const detailsQuery = useMemo<StatsQuery>(
    () => ({
      ...query,
      dimensions:
        dimension === 'region' || dimension === 'city'
          ? [dimension, 'country']
          : [dimension],
      filters: [
        ...query.filters,
        ...(searchValue
          ? [{ dimension, operator: 'contains' as const, values: [searchValue] }]
          : []),
      ],
      orderBy: [
        {
          column: sort?.id ?? metric,
          direction: sort?.desc === false ? 'asc' : 'desc',
        },
      ],
      limit: 25,
      ...(cursor === undefined ? { cursor: undefined } : { cursor }),
    }),
    [cursor, dimension, metric, query, searchValue, sort?.desc, sort?.id],
  )
  const result = useStats(detailsQuery)

  const columns = useMemo<ColumnDef<DetailRow>[]>(
    () => [
      {
        id: dimension,
        accessorFn: (row) => String(row[dimension] ?? ''),
        header: dimension,
        cell: ({ getValue, row }) => {
          const raw = String(getValue())
          const value = dimension === 'country' ? countryLabel(raw) : raw
          const country = String(row.original.country ?? (dimension === 'country' ? raw : ''))
          return (
            <span className="flex max-w-[18rem] items-center gap-2">
              {dimension === 'country' || dimension === 'region' || dimension === 'city'
                ? <Flag code={country} />
                : null}
              <span className="min-w-0 truncate" title={value}>{value}</span>
            </span>
          )
        },
      },
      {
        id: metric,
        accessorFn: (row) => Number(row[metric] ?? 0),
        header: metric,
        cell: ({ getValue }) => fmt.number(Number(getValue())),
      },
    ],
    [dimension, metric],
  )
  const table = useReactTable({
    columns,
    data: result.data?.rows ?? [],
    getCoreRowModel: getCoreRowModel(),
    manualPagination: true,
    manualSorting: true,
    onSortingChange: (updater) => {
      setSorting(updater)
      setCursor(undefined)
      setCursorHistory([])
    },
    state: { sorting },
  })

  const exportRows = async () => {
    setExportError(undefined)
    setExporting(true)
    try {
      await downloadStatsCSV(detailsQuery)
    } catch (problem) {
      setExportError(
        typeof problem === 'object' && problem && 'detail' in problem
          ? String(problem.detail)
          : 'CSV export failed.',
      )
    } finally {
      setExporting(false)
    }
  }

  return (
    <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-3xl">
      <DialogHeader>
        <DialogTitle>{title ?? dimension}</DialogTitle>
        <DialogDescription>Search, sort, and export the complete breakdown.</DialogDescription>
      </DialogHeader>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <Input
          aria-label={`Search ${dimension}`}
          className="sm:max-w-xs"
          type="search"
          value={search}
          onChange={(event) => {
            setSearch(event.target.value)
            setCursor(undefined)
            setCursorHistory([])
          }}
        />
        <Button type="button" variant="outline" disabled={exporting} onClick={exportRows}>
          <DownloadIcon data-icon="inline-start" />
          {exporting ? 'Exporting…' : 'CSV'}
        </Button>
      </div>
      {exportError ? <p className="text-sm text-destructive" role="alert">{exportError}</p> : null}
      {result.isError ? (
        <ErrorState detail={result.error?.detail} onRetry={() => void result.refetch()} />
      ) : result.isPending ? (
        <p className="py-12 text-center text-sm text-muted-foreground">Loading…</p>
      ) : table.getRowModel().rows.length === 0 ? (
        <EmptyState />
      ) : (
        <div className="min-w-0 overflow-x-auto rounded-lg border">
          <table aria-label={`${title ?? dimension} details`} className="w-full table-fixed text-sm">
            <thead className="bg-muted/50 text-left">
              {table.getHeaderGroups().map((headerGroup) => (
                <tr key={headerGroup.id}>
                  {headerGroup.headers.map((header) => (
                    <th key={header.id} className="px-3 py-2 font-medium">
                      <button
                        className="inline-flex max-w-full items-center gap-1 outline-none focus-visible:ring-2 focus-visible:ring-ring"
                        type="button"
                        onClick={header.column.getToggleSortingHandler()}
                      >
                        <span className="truncate">
                          {flexRender(header.column.columnDef.header, header.getContext())}
                        </span>
                        {header.column.getIsSorted() === 'asc' ? <ArrowUpIcon aria-label="ascending" /> : null}
                        {header.column.getIsSorted() === 'desc' ? <ArrowDownIcon aria-label="descending" /> : null}
                      </button>
                    </th>
                  ))}
                </tr>
              ))}
            </thead>
            <tbody>
              {table.getRowModel().rows.map((row) => (
                <tr key={row.id} className="border-t">
                  {row.getVisibleCells().map((cell) => (
                    <td key={cell.id} className="px-3 py-2 tabular-nums">
                      {flexRender(cell.column.columnDef.cell, cell.getContext())}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className="flex items-center justify-end gap-2">
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={cursorHistory.length === 0}
          onClick={() => {
            const previous = cursorHistory.at(-1)
            setCursor(previous || undefined)
            setCursorHistory((history) => history.slice(0, -1))
          }}
        >
          Previous
        </Button>
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={!result.data?.next_cursor}
          onClick={() => {
            if (!result.data?.next_cursor) return
            setCursorHistory((history) => [...history, cursor ?? ''])
            setCursor(result.data.next_cursor)
          }}
        >
          Next
        </Button>
      </div>
    </DialogContent>
  )
}

export function DetailsDialog(props: DetailsDialogProps) {
  const [open, setOpen] = useState(false)
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button type="button" size="sm" variant="ghost">
          Details
        </Button>
      </DialogTrigger>
      {open ? <DetailsContent {...props} /> : null}
    </Dialog>
  )
}
