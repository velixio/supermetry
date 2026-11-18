import { Link } from '@tanstack/react-router'
import { useState } from 'react'

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

import { useAppReleases } from '../api'
import { formatMs } from '../format'
import type { AppSearch } from '../search'
import { useRegressions } from './api'

/** Release-over-release p95 regressions: pick a base and a target release; the newest two are the default. */
export function RegressionsCard({ org, site, siteId, timezone, search }: { org: string; site: string; siteId: number; timezone: string; search: AppSearch }) {
  const releases = useAppReleases(siteId, { period: search.period }, timezone)
  const names = releases.data?.releases.map((item) => item.release) ?? []
  const [picked, setPicked] = useState<{ base?: string; target?: string }>({})
  const target = picked.target ?? names[0] ?? ''
  const base = picked.base ?? names[1] ?? ''
  const regressions = useRegressions(siteId, search, timezone, base, target)
  return (
    <Card>
      <CardHeader>
        <CardTitle>Regressions</CardTitle>
        <CardDescription>Transactions whose p95 grew by at least 20% from one release to another, with at least 30 samples in each.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {names.length < 2 ? <p className="text-sm text-muted-foreground">Two releases with transactions are needed to compare.</p> : (
          <>
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <Select value={base} onValueChange={(value) => setPicked({ ...picked, base: value })}>
                <SelectTrigger aria-label="Base release"><SelectValue /></SelectTrigger>
                <SelectContent>{names.map((name) => <SelectItem key={name} value={name}>{name}</SelectItem>)}</SelectContent>
              </Select>
              <span aria-hidden>to</span>
              <Select value={target} onValueChange={(value) => setPicked({ ...picked, target: value })}>
                <SelectTrigger aria-label="Target release"><SelectValue /></SelectTrigger>
                <SelectContent>{names.map((name) => <SelectItem key={name} value={name}>{name}</SelectItem>)}</SelectContent>
              </Select>
            </div>
            {regressions.isError ? <p role="alert" className="text-sm text-destructive">{regressions.error.message}</p> : null}
            {regressions.data && regressions.data.regressions.length === 0 ? <p className="text-sm text-muted-foreground">No regressions between these releases.</p> : null}
            {regressions.data && regressions.data.regressions.length > 0 ? (
              <Table aria-label="Regressions">
                <TableHeader><TableRow><TableHead>Transaction</TableHead><TableHead className="text-right">Base p95</TableHead><TableHead className="text-right">Target p95</TableHead><TableHead className="text-right">Change</TableHead></TableRow></TableHeader>
                <TableBody>
                  {regressions.data.regressions.map((row) => (
                    <TableRow key={`${row.name}|${row.op}`}>
                      <TableCell className="max-w-80 truncate font-medium"><Link className="underline" to="/app/$org/$site/performance/$transaction" params={{ org, site, transaction: row.name }} search={{ period: search.period }}>{row.name}</Link></TableCell>
                      <TableCell className="text-right tabular-nums">{formatMs(row.base_p95_ms)}</TableCell>
                      <TableCell className="text-right tabular-nums">{formatMs(row.target_p95_ms)}</TableCell>
                      <TableCell className="text-right tabular-nums text-destructive">+{(row.delta * 100).toFixed(0)}%</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            ) : null}
          </>
        )}
      </CardContent>
    </Card>
  )
}
