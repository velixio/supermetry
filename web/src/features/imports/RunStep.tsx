import { useEffect } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'

import { Button } from '@/components/ui/button'
import { StatusChip } from '@/components/StatusChip'
import { fmt } from '@/lib/format'
import { importWizard } from '@/messages/importWizard'

import { importKeys, importsApi, pollWhileInFlight, type SiteImport } from './api'

const t = importWizard.run

type Props = {
  siteId: number
  orgId: number
  /** Set once the server has accepted the import; before that, upload progress is shown. */
  importId?: number
  uploadPercent?: number
  onFinished: (item: SiteImport) => void
  onStartOver: () => void
}

export function RunStep({ siteId, orgId, importId, uploadPercent, onFinished, onStartOver }: Props) {
  const client = useQueryClient()
  const query = useQuery({
    queryKey: importKeys.one(importId ?? 0),
    queryFn: () => importsApi.get(importId as number, orgId),
    enabled: importId !== undefined,
    refetchInterval: (state) => pollWhileInFlight(state.state.data),
  })
  const item = query.data
  const status = item?.status

  useEffect(() => {
    if (status === 'completed' || status === 'failed') void client.invalidateQueries({ queryKey: importKeys.list(siteId) })
  }, [status, client, siteId])

  if (importId === undefined) {
    return (
      <div className="flex flex-col gap-3">
        <h3 className="font-medium">{t.heading}</h3>
        <Bar label={t.progressLabel} percent={uploadPercent ?? 0} />
        <p role="status" className="text-sm text-muted-foreground">
          {uploadPercent !== undefined && uploadPercent > 0 && uploadPercent < 100 ? t.uploading(uploadPercent) : t.queued}
        </p>
      </div>
    )
  }

  const progress = item?.progress
  const percent = status === 'completed' ? 100
    : progress && progress.total_days > 0 ? Math.min(100, Math.round((progress.done_days / progress.total_days) * 100)) : 0

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-3">
        <h3 className="font-medium">{t.heading}</h3>
        {status ? (
          <StatusChip variant={status === 'completed' ? 'success' : status === 'failed' ? 'danger' : 'neutral'}>{t.status[status]}</StatusChip>
        ) : null}
      </div>
      {query.isError && !item ? <p role="alert" className="text-sm text-destructive">{t.loadFailed}</p> : null}
      {status === 'failed' ? (
        <div className="flex flex-col gap-3">
          <p role="alert" className="text-sm text-destructive">{t.failed} {item?.error}</p>
          <p className="text-sm text-muted-foreground">{t.failedHint}</p>
          <Button type="button" variant="outline" className="self-start" onClick={onStartOver}>{importWizard.startOver}</Button>
        </div>
      ) : (
        <>
          <Bar label={t.progressLabel} percent={percent} />
          <p className="text-sm tabular-nums text-muted-foreground" role="status">
            {status === 'completed' ? t.completed
              : progress && progress.total_days > 0
                ? `${t.progressDays(progress.done_days, progress.total_days)}, ${t.progressRows(progress.rows)}`
                : item ? t.processing : t.queued}
          </p>
          {status === 'completed' && item ? (
            <Button type="button" className="self-start" onClick={() => onFinished(item)}>{t.toReview}</Button>
          ) : <p className="text-sm text-muted-foreground">{t.leaveNote}</p>}
          {item && item.rows > 0 && status === 'completed' ? <p className="text-sm text-muted-foreground">{fmt.number(item.rows)}</p> : null}
        </>
      )}
    </div>
  )
}

function Bar({ label, percent }: { label: string; percent: number }) {
  return (
    <div role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}
      className="h-2 w-full overflow-hidden rounded-full bg-muted">
      <div className="h-full bg-primary transition-[width]" style={{ width: `${percent}%` }} />
    </div>
  )
}
