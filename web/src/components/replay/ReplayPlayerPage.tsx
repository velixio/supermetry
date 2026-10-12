import type { ComponentProps } from 'react'
import { useCapability, AccessDenied } from '@/lib/authorization'
import { useCallback } from 'react'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'

import { ReplayPlayer } from './ReplayPlayer'
import { getReplayCSS, getReplayData } from '@/lib/replay-api'

function ReplayPlayerPageContent({ siteId, sessionId }: { siteId: number; sessionId: string }) {
  const load = useCallback((signal: AbortSignal) => getReplayData(siteId, sessionId, signal), [siteId, sessionId])
  const fetchCSS = useCallback((hash: string) => getReplayCSS(siteId, hash), [siteId])
  return (
    <PageContainer className="min-h-[calc(100dvh-10rem)]">
      <PageHeader title="Session replay" description={`Session ${sessionId}`} />
      <ReplayPlayer load={load} fetchCSS={fetchCSS} />
    </PageContainer>
  )
}

export function ReplayPlayerPage(props: ComponentProps<typeof ReplayPlayerPageContent>) {
  const allowed = useCapability('replay.read')
  if (!allowed) return <AccessDenied subject="watch replays" />
  return <ReplayPlayerPageContent {...props} />
}
