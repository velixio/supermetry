import { useState } from 'react'

import { InfoTip } from '@/components/InfoTip'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { fmt } from '@/lib/format'

import type { AppSearch } from '../search'
import { formatShare, useAdoption, type AdoptionDimension } from './api'

const DIMENSIONS: readonly [AdoptionDimension, string][] = [
  ['release', 'Release'], ['platform', 'Platform'], ['country', 'Country'], ['os', 'OS'], ['device', 'Device'],
]

export function AdoptionCard({ siteId, search, timezone }: { siteId: number; search: AppSearch; timezone: string }) {
  const [by, setBy] = useState<AdoptionDimension>('release')
  const adoption = useAdoption(siteId, search, timezone, by)
  const rows = [...(adoption.data?.totals ?? [])].sort((a, b) => b.installs - a.installs)
  return (
    <Card>
      <CardHeader>
        <CardTitle>Adoption <InfoTip label="About adoption">Installs active in this period. An install reporting two values counts in each, so shares can add up to more than 100%.</InfoTip></CardTitle>
        <Tabs value={by} onValueChange={(value) => setBy(value as AdoptionDimension)}>
          <TabsList aria-label="Adoption by">
            {DIMENSIONS.map(([value, label]) => <TabsTrigger key={value} value={value}>{label}</TabsTrigger>)}
          </TabsList>
        </Tabs>
      </CardHeader>
      <CardContent>
        {adoption.isPending ? <p role="status" className="text-sm text-muted-foreground">Loading adoption…</p> : null}
        {adoption.isError ? <p role="alert" className="text-sm text-destructive">{adoption.error.message}</p> : null}
        {adoption.data && rows.length === 0 ? <p className="rounded-lg bg-muted/30 p-8 text-center text-sm text-muted-foreground">No installs in this period.</p> : null}
        {rows.length > 0 ? (
          <Table aria-label={`Adoption by ${by}`}>
            <TableHeader><TableRow><TableHead>{DIMENSIONS.find(([value]) => value === by)?.[1]}</TableHead><TableHead className="text-right">Installs</TableHead><TableHead className="text-right">Share</TableHead></TableRow></TableHeader>
            <TableBody>
              {rows.map((row) => (
                <TableRow key={row.value}>
                  <TableCell>{row.value || 'Unknown'}</TableCell>
                  <TableCell className="text-right tabular-nums">{fmt.number(row.installs)}</TableCell>
                  <TableCell className="text-right tabular-nums">{formatShare(row.share)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : null}
      </CardContent>
    </Card>
  )
}
