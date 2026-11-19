import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { AlertTriangleIcon, BellOffIcon, CheckCircle2Icon, Clock3Icon, RotateCcwIcon } from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { IncidentList, useIncidents } from '@/features/apps/alerts/IncidentList'
import { en } from '@/messages/en'
import { uiAlerts as t } from '@/messages/uiAlerts'
import { type DeliveryStatus, type HistoryFilters, useNotificationHistory, useRetryNotification } from './notificationHistoryApi'

/** Channel kinds as people write them; "none" marks a notice withheld by a silence. */
export const channelKindLabels: Record<string, string> = t.common.channelKind

const statusIcon = {
  pending: Clock3Icon,
  succeeded: CheckCircle2Icon,
  failed: AlertTriangleIcon,
  suppressed: BellOffIcon,
} as const

export function NotificationHistoryPage({ siteId, orgId, filters, onFilters }: {
  siteId: number
  orgId: number
  filters: HistoryFilters
  onFilters: (filters: HistoryFilters) => void
}) {
  const history = useNotificationHistory(siteId, orgId, filters)
  const retry = useRetryNotification(siteId, orgId)
  const items = history.data?.data ?? []
  const incidents = useIncidents(siteId, orgId, filters.alertId)

  return (
    <PageContainer width="narrow">
      <PageHeader title={en.notify.historyTitle} description={en.notify.historyDescription} />
      <section aria-label={t.history.filters} className="grid gap-3 rounded-lg bg-card p-4 sm:grid-cols-3">
        <label className="flex flex-col gap-1 text-sm font-medium">
          {t.history.status}
          <select className="max-sm:min-h-11 rounded-lg border border-input bg-background px-3" value={filters.status ?? ''} onChange={(event) => onFilters({ ...filters, status: event.target.value as DeliveryStatus || undefined })}>
            <option value="">{t.history.allStatuses}</option><option value="pending">{t.history.statuses.pending}</option><option value="succeeded">{t.history.statuses.succeeded}</option><option value="failed">{t.history.statuses.failed}</option><option value="suppressed">{t.history.statuses.suppressed}</option>
          </select>
        </label>
        <label className="flex flex-col gap-1 text-sm font-medium">
          {t.history.channel}
          <select className="max-sm:min-h-11 rounded-lg border border-input bg-background px-3" value={filters.channel ?? ''} onChange={(event) => onFilters({ ...filters, channel: event.target.value || undefined })}>
            <option value="">{t.history.allChannels}</option>{Object.entries(t.common.channelKind).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-sm font-medium">
          {t.history.alertId}
          <Input className="max-sm:min-h-11" min="1" placeholder={t.history.anyAlert} type="number" value={filters.alertId ?? ''} onChange={(event) => onFilters({ ...filters, alertId: event.target.value ? Number(event.target.value) : undefined })} />
        </label>
      </section>
      {history.error || retry.error ? <p role="alert" className="text-sm text-destructive">{history.error?.message ?? retry.error?.message}</p> : null}
      {history.isPending ? <p role="status" className="rounded-lg bg-card p-5 text-sm text-muted-foreground">{t.history.loading}</p> : null}
      {!history.isPending && items.length === 0 ? <p className="rounded-lg bg-card p-5 text-sm text-muted-foreground">{t.history.empty}</p> : null}
      {incidents.data && incidents.data.data.length > 0 ? (
        <section aria-label={t.history.incidentsRegion} className="flex flex-col gap-3">
          <h2 className="text-sm font-medium">{t.history.incidentsOf(filters.alertId ?? 0)}</h2>
          <IncidentList incidents={incidents.data.data} />
        </section>
      ) : null}
      <ol aria-label={t.history.deliveries} className="flex flex-col gap-3">
        {items.map((item) => {
          const Icon = statusIcon[item.status]
          return (
            <li key={item.id} className="grid gap-3 rounded-lg bg-card p-4 md:grid-cols-[minmax(0,1fr)_auto] md:items-center">
              <div className="min-w-0 space-y-2">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant={item.status === 'failed' ? 'destructive' : 'secondary'} className="gap-1 capitalize"><Icon aria-hidden className="size-3.5" />{t.history.statuses[item.status].toLocaleLowerCase()}</Badge>
                  <span className="font-medium">{item.channel_name}</span>
                  <span className="text-sm text-muted-foreground">{channelKindLabels[item.channel_kind] ?? item.channel_kind}</span>
                </div>
                <p className="text-sm text-muted-foreground"><button type="button" className="underline" onClick={() => onFilters({ ...filters, alertId: item.alert_id })}>{t.history.alert(item.alert_id)}</button> · {t.history.attempts(item.attempts)} · {t.history.fired(new Date(item.fired_at).toLocaleString())}</p>
                {item.error_text ? <p className="break-words text-sm text-destructive">{item.error_text}</p> : null}
              </div>
              {item.status === 'failed' ? <Button className="max-sm:min-h-11" variant="outline" disabled={retry.isPending} onClick={() => retry.mutate(item.id)}><RotateCcwIcon aria-hidden />{t.history.retry}</Button> : null}
            </li>
          )
        })}
      </ol>
    </PageContainer>
  )
}
