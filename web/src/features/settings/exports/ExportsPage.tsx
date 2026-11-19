import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { MoreHorizontalIcon, PlusIcon } from 'lucide-react'

import { ConfirmDangerDialog } from '@/components/ConfirmDangerDialog'
import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { StatusChip, type StatusChipVariant } from '@/components/StatusChip'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { fmt } from '@/lib/format'
import { activeLocale } from '@/lib/i18n/intl'
import { useReauth } from '@/lib/useReauth'
import { uiSettings } from '@/messages/uiSettings'

import { privateRequest } from '../privateApi'
import { relativeTime } from '../keys/keyStatus'
import { ExportScheduleForm } from './ExportScheduleForm'
import { SettingsBundle } from './SettingsBundle'
import type { ExportRun, ExportSchedule } from './types'

type Site = { id: number; name: string }

type Chip = { label: string; variant: StatusChipVariant }

export function scheduleChip(schedule: Pick<ExportSchedule, 'enabled' | 'last_status'>): Chip {
  const t = uiSettings.exports.status
  if (!schedule.enabled) return { label: t.paused, variant: 'neutral' }
  switch (schedule.last_status) {
    case 'succeeded': return { label: t.succeeded, variant: 'success' }
    case 'failed': return { label: t.failed, variant: 'danger' }
    case 'skipped': return { label: t.skipped, variant: 'warning' }
    default: return { label: t.never, variant: 'neutral' }
  }
}

export function ExportsPage({ orgId, role, sites }: { orgId: number; role: string; sites: Site[] }) {
  const t = uiSettings.exports
  const queryClient = useQueryClient()
  const { withReauth, reauthDialog } = useReauth()
  const [editing, setEditing] = useState<ExportSchedule | 'new' | null>(null)
  const [deleting, setDeleting] = useState<ExportSchedule | null>(null)
  const [history, setHistory] = useState<ExportSchedule | null>(null)
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')
  const canManage = role === 'owner' || role === 'admin'
  const base = `/api/private/orgs/${orgId}/export-schedules`
  const queryKey = ['export-schedules', orgId]
  const schedules = useQuery({
    queryKey,
    enabled: canManage,
    queryFn: () => privateRequest<{ schedules: ExportSchedule[] }>('GET', base, { orgId }),
  })
  const refresh = () => void queryClient.invalidateQueries({ queryKey })
  const run = useMutation({ mutationFn: (id: number) => privateRequest<void>('POST', `${base}/${id}/run`, { orgId }) })
  const remove = useMutation({ mutationFn: (id: number) => privateRequest<void>('DELETE', `${base}/${id}`, { orgId }) })

  if (!canManage) return <p role="alert" className="text-sm text-muted-foreground">{t.unauthorized}</p>
  const list = schedules.data?.schedules ?? []
  const siteName = (id: number | null) => (id === null ? t.allSites : sites.find((site) => site.id === id)?.name ?? `#${id}`)

  async function runNow(schedule: ExportSchedule) {
    setError('')
    setNotice('')
    try {
      await withReauth(() => run.mutateAsync(schedule.id))
      setNotice(t.runQueued)
      refresh()
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure))
    }
  }

  return (
    <PageContainer className="max-w-[1200px] gap-6">
      <PageHeader
        title={t.title}
        description={t.description}
        actions={<Button type="button" onClick={() => setEditing('new')}><PlusIcon data-icon="inline-start" />{t.newSchedule}</Button>}
      />
      <section aria-labelledby="schedules-title" className="flex flex-col gap-3">
        <h2 id="schedules-title" className="text-base font-semibold">{t.schedulesTitle}</h2>
        {schedules.error ? <p role="alert" className="text-sm text-destructive">{schedules.error instanceof Error ? schedules.error.message : String(schedules.error)}</p> : null}
        {error ? <p role="alert" className="text-sm text-destructive">{error}</p> : null}
        {notice ? <p role="status" className="text-sm text-muted-foreground">{notice}</p> : null}
        {schedules.isSuccess && list.length === 0 ? <p className="text-sm text-muted-foreground">{t.empty}</p> : null}
        {list.length > 0 ? (
          <div className="rounded-lg bg-card text-card-foreground">
            <Table aria-label={t.tableLabel}>
              <TableHeader>
                <TableRow>
                  <TableHead>{t.columns.name}</TableHead>
                  <TableHead>{t.columns.destination}</TableHead>
                  <TableHead>{t.columns.frequency}</TableHead>
                  <TableHead>{t.columns.lastRun}</TableHead>
                  <TableHead>{t.columns.status}</TableHead>
                  <TableHead className="w-12"><span className="sr-only">{t.columns.actions}</span></TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.map((schedule) => {
                  const chip = scheduleChip(schedule)
                  return (
                    <TableRow key={schedule.id}>
                      <TableCell className="font-medium">{schedule.name}<div className="text-xs font-normal text-muted-foreground">{siteName(schedule.site_id)}</div></TableCell>
                      <TableCell className="text-sm">{schedule.bucket}{schedule.prefix ? `/${schedule.prefix}` : ''}</TableCell>
                      <TableCell>{t.frequency[schedule.frequency]}</TableCell>
                      <TableCell>{schedule.last_run_at ? relativeTime(schedule.last_run_at, activeLocale()) : '—'}</TableCell>
                      <TableCell>
                        <StatusChip variant={chip.variant}>{chip.label}</StatusChip>
                        {schedule.last_status === 'failed' && schedule.last_error ? <div className="mt-1 max-w-xs text-xs break-words text-muted-foreground">{schedule.last_error}</div> : null}
                      </TableCell>
                      <TableCell>
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button type="button" variant="ghost" size="icon" aria-label={uiSettings.keys.rowMenu(schedule.name)}><MoreHorizontalIcon aria-hidden="true" /></Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem onSelect={() => void runNow(schedule)}>{t.runNow}</DropdownMenuItem>
                            <DropdownMenuItem onSelect={() => setHistory(schedule)}>{t.history}</DropdownMenuItem>
                            <DropdownMenuItem onSelect={() => setEditing(schedule)}>{t.edit}</DropdownMenuItem>
                            <DropdownMenuSeparator />
                            <DropdownMenuItem variant="destructive" onSelect={() => setDeleting(schedule)}>{t.delete}</DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>
        ) : null}
      </section>
      <SettingsBundle orgId={orgId} />
      <ExportScheduleForm
        orgId={orgId}
        sites={sites}
        schedule={editing === 'new' ? null : editing}
        open={editing !== null}
        onOpenChange={(open) => { if (!open) setEditing(null) }}
        onSaved={refresh}
      />
      <RunHistory orgId={orgId} schedule={history} onClose={() => setHistory(null)} />
      <ConfirmDangerDialog
        trigger={<span hidden />}
        open={deleting !== null}
        onOpenChange={(open) => { if (!open) setDeleting(null) }}
        title={t.deleteTitle(deleting?.name ?? '')}
        description={t.deleteBody}
        confirmLabel={t.deleteConfirm}
        onConfirm={async () => {
          if (!deleting) return
          await withReauth(() => remove.mutateAsync(deleting.id))
          refresh()
        }}
      />
      {reauthDialog}
    </PageContainer>
  )
}

function RunHistory({ orgId, schedule, onClose }: { orgId: number; schedule: ExportSchedule | null; onClose: () => void }) {
  const t = uiSettings.exports
  const runs = useQuery({
    queryKey: ['export-runs', orgId, schedule?.id],
    enabled: schedule !== null,
    queryFn: () => privateRequest<{ runs: ExportRun[] }>('GET', `/api/private/orgs/${orgId}/export-schedules/${schedule!.id}/runs`, { orgId }),
  })
  const variant: Record<ExportRun['status'], StatusChipVariant> = { running: 'neutral', succeeded: 'success', failed: 'danger', skipped: 'warning' }
  return (
    <Sheet open={schedule !== null} onOpenChange={(open) => { if (!open) onClose() }}>
      <SheetContent className="sm:max-w-lg">
        <SheetHeader>
          <SheetTitle>{t.historyTitle(schedule?.name ?? '')}</SheetTitle>
          <SheetDescription>{schedule ? t.nextRun(fmt.date(schedule.next_run_at, 'UTC')) : ''}</SheetDescription>
        </SheetHeader>
        <div className="px-4 pb-4">
          {runs.isSuccess && runs.data.runs.length === 0 ? <p className="text-sm text-muted-foreground">{t.historyEmpty}</p> : null}
          {runs.data?.runs.length ? (
            <ul className="flex flex-col gap-3">
              {runs.data.runs.map((item) => (
                <li key={item.id} className="flex flex-col gap-1 text-sm">
                  <div className="flex items-center justify-between gap-2">
                    <span>{fmt.date(item.started_at, 'UTC')}</span>
                    <StatusChip variant={variant[item.status]}>{t.status[item.status]}</StatusChip>
                  </div>
                  <span className="text-xs text-muted-foreground">{item.from_day && item.to_day ? `${item.from_day} → ${item.to_day} · ` : ''}{fmt.bytes(item.bytes)}</span>
                  {item.error ? <span className="text-xs break-words text-destructive">{item.error}</span> : null}
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      </SheetContent>
    </Sheet>
  )
}
