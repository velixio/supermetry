import type { ComponentProps } from 'react'
import { useCapability, AccessDenied } from '@/lib/authorization'
import { useCallback } from 'react'

import { ReplayPlayer } from './ReplayPlayer'
import { getReplayCSS, getReplayData } from '@/lib/replay-api'

function ReplayPlayerPageContent({ siteId, sessionId }: { siteId: number; sessionId: string }) {
	const load = useCallback((signal: AbortSignal) => getReplayData(siteId, sessionId, signal), [siteId, sessionId])
	const fetchCSS = useCallback((hash: string) => getReplayCSS(siteId, hash), [siteId])
  return (
    <div className="flex min-w-0 flex-col gap-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-xl font-semibold tracking-tight">Session replay</h1>
        <p className="text-sm text-muted-foreground">Session {sessionId}</p>
      </header>
		<ReplayPlayer load={load} fetchCSS={fetchCSS} />
    </div>
  )
}

export function ReplayPlayerPage(props: ComponentProps<typeof ReplayPlayerPageContent>) {
  const allowed = useCapability('replay.read')
  if (!allowed) return <AccessDenied subject="watch replays" />
  return <ReplayPlayerPageContent {...props} />
}
