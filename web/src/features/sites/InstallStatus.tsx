import { Badge } from '@/components/ui/badge'
import { activeLocale } from '@/lib/i18n/intl'

export type InstallStatus = 'waiting' | 'receiving' | 'quiet'

function relativeTime(value: string, now: Date): string {
  const seconds = Math.round((new Date(value).getTime() - now.getTime()) / 1_000)
  const absolute = Math.abs(seconds)
  const formatter = new Intl.RelativeTimeFormat(activeLocale(), { numeric: 'always' })
  if (absolute < 60) return formatter.format(seconds, 'second')
  const minutes = Math.round(seconds / 60)
  if (Math.abs(minutes) < 60) return formatter.format(minutes, 'minute')
  const hours = Math.round(minutes / 60)
  if (Math.abs(hours) < 24) return formatter.format(hours, 'hour')
  return formatter.format(Math.round(hours / 24), 'day')
}

const labels: Record<InstallStatus, string> = {
  waiting: 'Waiting',
  receiving: 'Receiving',
  quiet: 'Quiet',
}

export function InstallStatusView({
  status,
  lastSeenAt,
  now = new Date(),
}: {
  status?: InstallStatus
  lastSeenAt?: string | null
  now?: Date
}) {
  const resolved = status ?? (lastSeenAt ? 'receiving' : 'waiting')
  const detail = lastSeenAt
    ? `Last event ${relativeTime(lastSeenAt, now)}`
    : 'Waiting for the first accepted event.'
  return (
    <div className="flex flex-wrap items-center gap-2" role="status">
      <Badge variant={resolved === 'receiving' ? 'default' : resolved === 'quiet' ? 'secondary' : 'outline'}>
        {labels[resolved]}
      </Badge>
      <span className="text-xs text-muted-foreground">{detail}</span>
    </div>
  )
}
