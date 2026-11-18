import { Card, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { fmt } from '@/lib/format'

import type { AppSearch } from '../search'
import { formatSeconds, useEngagement } from './api'

function Tile({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <Card>
      <CardHeader>
        <CardDescription>{label}</CardDescription>
        <CardTitle className="text-2xl tabular-nums">{children}</CardTitle>
      </CardHeader>
    </Card>
  )
}

const decimal = (value: number | null) => (value === null ? 'n/a' : value.toFixed(1))

export function EngagementCard({ siteId, search, timezone }: { siteId: number; search: AppSearch; timezone: string }) {
  const engagement = useEngagement(siteId, search, timezone)
  if (engagement.isPending) return <p role="status" className="text-sm text-muted-foreground">Loading engagement…</p>
  if (engagement.isError) return <p role="alert" className="text-sm text-destructive">{engagement.error.message}</p>
  const data = engagement.data
  return (
    <div className="grid grid-cols-2 gap-4 md:grid-cols-5" aria-label="Engagement">
      <Tile label="Sessions">{fmt.number(data.sessions)}</Tile>
      <Tile label="Sessions per user">{decimal(data.sessions_per_user)}</Tile>
      <Tile label="Mean session length">{formatSeconds(data.mean_session_seconds)}</Tile>
      <Tile label="Median session length">{formatSeconds(data.median_session_seconds)}</Tile>
      <Tile label="Screens per session">{decimal(data.screens_per_session)}</Tile>
    </div>
  )
}
