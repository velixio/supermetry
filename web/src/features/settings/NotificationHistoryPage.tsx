import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { AlertTriangleIcon, CheckCircle2Icon, Clock3Icon, RotateCcwIcon } from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { en } from '@/messages/en'
import { type DeliveryStatus, type HistoryFilters, useNotificationHistory, useRetryNotification } from './notificationHistoryApi'

const statusIcon = {
  pending: Clock3Icon,
  succeeded: CheckCircle2Icon,
  failed: AlertTriangleIcon,
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

  return (
    <PageContainer width="narrow">
      <PageHeader title={en.notify.historyTitle} description={en.notify.historyDescription} />
      <section aria-label="Delivery filters" className="grid gap-3 rounded-xl border bg-card p-4 sm:grid-cols-3">
        <label className="flex flex-col gap-1 text-sm font-medium">
          Status
          <select className="h-11 rounded-lg border border-input bg-background px-3" value={filters.status ?? ''} onChange={(event) => onFilters({ ...filters, status: event.target.value as DeliveryStatus || undefined })}>
            <option value="">All statuses</option><option value="pending">Pending</option><option value="succeeded">Succeeded</option><option value="failed">Failed</option>
          </select>
        </label>
        <label className="flex flex-col gap-1 text-sm font-medium">
          Channel
          <select className="h-11 rounded-lg border border-input bg-background px-3" value={filters.channel ?? ''} onChange={(event) => onFilters({ ...filters, channel: event.target.value || undefined })}>
            <option value="">All channels</option><option value="email">Email</option><option value="slack">Slack</option><option value="discord">Discord</option><option value="telegram">Telegram</option><option value="webhook">Webhook</option>
          </select>
        </label>
        <label className="flex flex-col gap-1 text-sm font-medium">
          Alert ID
          <Input className="h-11" min="1" placeholder="Any alert" type="number" value={filters.alertId ?? ''} onChange={(event) => onFilters({ ...filters, alertId: event.target.value ? Number(event.target.value) : undefined })} />
        </label>
      </section>
      {history.error || retry.error ? <p role="alert" className="text-sm text-destructive">{history.error?.message ?? retry.error?.message}</p> : null}
      {history.isPending ? <p role="status" className="rounded-xl border p-5 text-sm text-muted-foreground">Loading delivery history…</p> : null}
      {!history.isPending && items.length === 0 ? <p className="rounded-xl border bg-card p-5 text-sm text-muted-foreground">No deliveries match these filters.</p> : null}
      <ol aria-label="Alert deliveries" className="flex flex-col gap-3">
        {items.map((item) => {
          const Icon = statusIcon[item.status]
          return (
            <li key={item.id} className="grid gap-3 rounded-xl border bg-card p-4 md:grid-cols-[minmax(0,1fr)_auto] md:items-center">
              <div className="min-w-0 space-y-2">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant={item.status === 'failed' ? 'destructive' : 'secondary'} className="gap-1 capitalize"><Icon aria-hidden className="size-3.5" />{item.status}</Badge>
                  <span className="font-medium">{item.channel_name}</span>
                  <span className="text-sm capitalize text-muted-foreground">{item.channel_kind}</span>
                </div>
                <p className="text-sm text-muted-foreground">Alert {item.alert_id} · {item.attempts} {item.attempts === 1 ? 'attempt' : 'attempts'} · Fired {new Date(item.fired_at).toLocaleString()}</p>
                {item.error_text ? <p className="break-words text-sm text-destructive">{item.error_text}</p> : null}
              </div>
              {item.status === 'failed' ? <Button className="min-h-11" variant="outline" disabled={retry.isPending} onClick={() => retry.mutate(item.id)}><RotateCcwIcon aria-hidden />Retry delivery</Button> : null}
            </li>
          )
        })}
      </ol>
    </PageContainer>
  )
}
