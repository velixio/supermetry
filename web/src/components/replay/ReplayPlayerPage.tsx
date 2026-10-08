import { useCallback } from 'react'

import { ReplayPlayer } from './ReplayPlayer'
import { getReplayData } from '@/lib/replay-api'

export function ReplayPlayerPage({ siteId, sessionId }: { siteId: number; sessionId: string }) {
  const load = useCallback((signal: AbortSignal) => getReplayData(siteId, sessionId, signal), [siteId, sessionId])
  return (
    <main className="flex min-w-0 flex-col gap-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-xl font-semibold tracking-tight">Session replay</h1>
        <p className="text-sm text-muted-foreground">Session {sessionId}</p>
      </header>
      <ReplayPlayer load={load} />
    </main>
  )
}
