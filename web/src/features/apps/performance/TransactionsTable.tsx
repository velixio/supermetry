import { Link } from '@tanstack/react-router'
import { useMemo, useState } from 'react'

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { fmt } from '@/lib/format'

import { formatMs, formatRate } from '../format'
import type { AppSearch } from '../search'
import type { TransactionRowData, TransactionSort } from './api'

const COLUMNS: { key: TransactionSort; label: string }[] = [
  { key: 'p50', label: 'p50' },
  { key: 'p95', label: 'p95' },
  { key: 'count', label: 'Count' },
  { key: 'errors', label: 'Errors' },
]

const value = (row: TransactionRowData, key: TransactionSort): number => (key === 'p50' ? row.p50_ms : key === 'p95' ? row.p95_ms : key === 'count' ? row.count : row.errors)

/** Transactions with percentiles, throughput and error rate; sortable by p50, p95, count and errors (slowest p95 first by default). */
export function TransactionsTable({ org, site, rows, search, label = 'Transactions' }: { org: string; site: string; rows: TransactionRowData[]; search: AppSearch; label?: string }) {
  const [sort, setSort] = useState<TransactionSort>('p95')
  const sorted = useMemo(() => [...rows].sort((a, b) => value(b, sort) - value(a, sort) || a.name.localeCompare(b.name)), [rows, sort])
  return (
    <Table aria-label={label}>
      <TableHeader>
        <TableRow>
          <TableHead>Transaction</TableHead>
          <TableHead>Op</TableHead>
          {COLUMNS.slice(0, 2).map((column) => <SortHead key={column.key} column={column} sort={sort} onSort={setSort} />)}
          <TableHead className="text-right">p99</TableHead>
          <SortHead column={COLUMNS[2]} sort={sort} onSort={setSort} />
          <TableHead className="text-right">Per minute</TableHead>
          <SortHead column={COLUMNS[3]} sort={sort} onSort={setSort} />
          <TableHead className="text-right">Error rate</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {sorted.map((row) => (
          <TableRow key={`${row.name}|${row.op}`}>
            <TableCell className="max-w-80 truncate font-medium">
              <Link className="underline" to="/app/$org/$site/performance/$transaction" params={{ org, site, transaction: row.name }} search={{ period: search.period, release: search.release, environment: search.environment, platform: search.platform }}>{row.name}</Link>
            </TableCell>
            <TableCell className="text-muted-foreground">{row.op || 'n/a'}</TableCell>
            <TableCell className="text-right tabular-nums">{formatMs(row.p50_ms)}</TableCell>
            <TableCell className="text-right tabular-nums">{formatMs(row.p95_ms)}</TableCell>
            <TableCell className="text-right tabular-nums">{formatMs(row.p99_ms)}</TableCell>
            <TableCell className="text-right tabular-nums">{fmt.number(row.count)}</TableCell>
            <TableCell className="text-right tabular-nums">{row.throughput_per_min.toFixed(2)}</TableCell>
            <TableCell className="text-right tabular-nums">{fmt.number(row.errors)}</TableCell>
            <TableCell className="text-right tabular-nums">{formatRate(row.error_rate)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function SortHead({ column, sort, onSort }: { column: { key: TransactionSort; label: string }; sort: TransactionSort; onSort: (key: TransactionSort) => void }) {
  return (
    <TableHead className="text-right" aria-sort={sort === column.key ? 'descending' : 'none'}>
      <button type="button" className={sort === column.key ? 'font-semibold underline' : ''} onClick={() => onSort(column.key)}>{column.label}</button>
    </TableHead>
  )
}
