import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Button } from '@/components/ui/button'
import { privateRequest } from '@/features/settings/privateApi'
import { uiAlerts as t } from '@/messages/uiAlerts'
import type { components } from '@/lib/api.gen'

export type AppMonitor = components['schemas']['AppMonitor']
type MonitorList = components['schemas']['AppMonitorList']
type Checkins = components['schemas']['AppMonitorCheckins']

const base = (siteId: number) => `/api/private/sites/${siteId}/app/monitors`

export function useMonitors(siteId: number) {
  return useQuery({ queryKey: ['app-monitors', siteId], queryFn: () => privateRequest<MonitorList>('GET', base(siteId)) })
}

export function useMonitorCheckins(siteId: number, monitorId: number | null) {
  return useQuery({
    queryKey: ['app-monitor-checkins', siteId, monitorId],
    enabled: monitorId !== null,
    queryFn: () => privateRequest<Checkins>('GET', `${base(siteId)}/${monitorId}/checkins?limit=20`),
  })
}

const STATUS_LABEL: Record<AppMonitor['status'], string> = t.monitors.status

/** "every 2 hours" or the cron expression with its time zone. */
export function describeSchedule(m: Pick<AppMonitor, 'schedule_type' | 'schedule' | 'timezone'>): string {
  if (m.schedule_type === 'interval') {
    const [n, unit] = m.schedule.split(' ')
    return t.monitors.every(n, unit)
  }
  return `${m.schedule} (${m.timezone})`
}

function when(value: string | null): string {
  return value ? new Date(value).toLocaleString() : t.monitors.never
}

export function MonitorsPage({ siteId, canEdit }: { siteId: number; canEdit: boolean }) {
  const monitors = useMonitors(siteId)
  const client = useQueryClient()
  const refresh = () => client.invalidateQueries({ queryKey: ['app-monitors', siteId] })
  const toggle = useMutation({
    mutationFn: (m: AppMonitor) => privateRequest<AppMonitor>('PATCH', `${base(siteId)}/${m.id}`, { body: { disabled: m.status !== 'disabled' } }),
    onSuccess: refresh,
  })
  const remove = useMutation({
    mutationFn: (m: AppMonitor) => privateRequest<void>('DELETE', `${base(siteId)}/${m.id}`),
    onSuccess: refresh,
  })
  const error = monitors.error ?? toggle.error ?? remove.error
  return (
    <PageContainer width="narrow">
      <PageHeader
        title={t.monitors.title}
        description={t.monitors.description}
      />
      {error ? <p role="alert" className="text-sm text-destructive">{error.message}</p> : null}
      {monitors.data?.monitors.length === 0 ? (
        <p className="rounded-lg border bg-card p-5 text-sm text-muted-foreground">
          {t.monitors.empty}
        </p>
      ) : null}
      <div className="flex flex-col gap-3">
        {monitors.data?.monitors.map((m) => (
          <section className="flex flex-wrap items-center justify-between gap-3 rounded-lg border bg-card p-4" key={m.id} aria-label={t.monitors.monitorAria(m.slug)}>
            <div>
              <h2 className="font-medium">{m.slug} <span className="text-sm font-normal text-muted-foreground">{m.environment}</span></h2>
              <p className="text-sm text-muted-foreground">{STATUS_LABEL[m.status]} · {describeSchedule(m)}</p>
              <p className="text-xs text-muted-foreground">{t.monitors.lastCheckin(when(m.last_checkin_at), m.status === 'disabled' ? t.monitors.notScheduled : when(m.next_expected_at))}</p>
            </div>
            {canEdit ? (
              <div className="flex gap-2">
                <Button variant="outline" disabled={toggle.isPending} onClick={() => toggle.mutate(m)}>{m.status === 'disabled' ? t.monitors.enable : t.monitors.disable}</Button>
                <Button variant="outline" disabled={remove.isPending} onClick={() => remove.mutate(m)}>{t.common.delete}</Button>
              </div>
            ) : null}
          </section>
        ))}
      </div>
    </PageContainer>
  )
}
