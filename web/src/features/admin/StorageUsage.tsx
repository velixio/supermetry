import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { fmt } from '@/lib/format'
import { uiFlows } from '@/messages/uiFlows'
import { formatDate } from './AdminCommon'

export interface StorageUsageValue {
  estimated_storage_bytes: number
  database_bytes: number
  replay_bytes: number
  measured_at: string
  measurement_version: number
  unmeasured_sites?: number
}

export function StorageUsage({ name, storage }: { name: string; storage?: StorageUsageValue | null }) {
  if (!storage) return <span className="text-muted-foreground">{uiFlows.admin.storageCalculating}</span>
  const partial = (storage.unmeasured_sites ?? 0) > 0
  const measured = fmt.bytes(storage.estimated_storage_bytes)
  const total = partial ? uiFlows.admin.storageAtLeast(measured) : measured

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button type="button" variant="ghost" size="sm" className="-mx-2 min-h-11 px-2" aria-label={uiFlows.admin.storageDetails(name, total)}>
          <span className="tabular-nums">{total}</span>
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[min(18rem,calc(100vw-2rem))]">
        <p className="font-medium">{uiFlows.admin.storage}</p>
        <dl className="mt-2 grid grid-cols-[1fr_auto] gap-x-4 gap-y-1 text-sm">
          <dt className="text-muted-foreground">{uiFlows.admin.storageDatabase}</dt>
          <dd className="tabular-nums">{fmt.bytes(storage.database_bytes)}</dd>
          <dt className="text-muted-foreground">{uiFlows.admin.storageReplay}</dt>
          <dd className="tabular-nums">{fmt.bytes(storage.replay_bytes)}</dd>
        </dl>
        {partial && <p className="mt-3 text-xs text-muted-foreground">{uiFlows.admin.storageUnmeasured(storage.unmeasured_sites ?? 0)}</p>}
        <p className="mt-3 text-xs text-muted-foreground">{uiFlows.admin.storageUpdated(formatDate(storage.measured_at))}</p>
        <p className="mt-1 text-xs text-muted-foreground">{uiFlows.admin.storageEstimateNote}</p>
      </PopoverContent>
    </Popover>
  )
}
