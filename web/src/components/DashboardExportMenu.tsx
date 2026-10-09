import { isAnonymousDimensionAllowed } from '@/lib/tracking-policy'
import { useCapability } from '@/lib/authorization'
import { useState } from 'react'
import { ChevronDownIcon, DownloadIcon } from 'lucide-react'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { downloadStatsCSV } from '@/lib/csv'
import type { StatsQuery } from '@/lib/stats'

type ExportOption = {
  label: string
  dimension: string
  metric: StatsQuery['metrics'][number]
}

const exportOptions: ExportOption[] = [
  { label: 'Timeseries', dimension: 'time', metric: 'visitors' },
  { label: 'Pages', dimension: 'page', metric: 'visitors' },
  { label: 'Entry', dimension: 'entry', metric: 'visitors' },
  { label: 'Exit', dimension: 'exit', metric: 'visitors' },
  { label: 'Sources', dimension: 'source', metric: 'visitors' },
  { label: 'UTM', dimension: 'utm_source', metric: 'visitors' },
  { label: 'Countries', dimension: 'country', metric: 'visitors' },
  { label: 'Regions', dimension: 'region', metric: 'visitors' },
  { label: 'Cities', dimension: 'city', metric: 'visitors' },
  { label: 'Browsers', dimension: 'browser', metric: 'visitors' },
  { label: 'OS', dimension: 'os', metric: 'visitors' },
  { label: 'Devices', dimension: 'device', metric: 'visitors' },
  { label: 'Events', dimension: 'event', metric: 'events' },
]

export function DashboardExportMenu({ query, anonymous = false }: { query: StatsQuery; anonymous?: boolean }) {
  const canExport = useCapability('data.export')
  const [exporting, setExporting] = useState(false)

  const exportCSV = async (option: ExportOption) => {
    if (!canExport) return
    if (anonymous && option.dimension !== 'page' && !isAnonymousDimensionAllowed(option.dimension)) return
    if (anonymous && option.dimension === 'page' && query.filters.length > 0) return
    setExporting(true)
    try {
      await downloadStatsCSV({
        ...query,
        dimensions: [option.dimension],
        metrics: [option.metric],
        ...(anonymous && option.dimension === 'page' ? { includeImported: false } : {}),
      })
    } catch (problem) {
      toast.error(
        typeof problem === 'object' && problem && 'detail' in problem
          ? String(problem.detail)
          : 'CSV export failed.',
      )
    } finally {
      setExporting(false)
    }
  }

  if (!canExport) return null
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button type="button" variant="outline" disabled={exporting}>
          <DownloadIcon data-icon="inline-start" />
          {exporting ? 'Exporting…' : 'Export CSV'}
          <ChevronDownIcon data-icon="inline-end" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {anonymous ? <p className="max-w-64 px-2 py-1.5 text-xs text-muted-foreground">Exports include safe aggregate categories. Verified pages exclude imports and unverified history and require no active filters.</p> : null}
        <DropdownMenuGroup>
          {exportOptions.filter((option) => !anonymous || isAnonymousDimensionAllowed(option.dimension) || option.dimension === 'page').map((option) => (
            <DropdownMenuItem
              key={option.dimension}
              disabled={exporting || (anonymous && option.dimension === 'page' && query.filters.length > 0)}
              onSelect={() => void exportCSV(option)}
            >
              {anonymous && option.dimension === 'page' ? 'Verified pages (partial)' : option.label}
            </DropdownMenuItem>
          ))}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
