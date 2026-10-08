import { useEffect, useRef, useState } from 'react'

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import type { ReplayData } from '@/lib/replay-api'

type MountPlayer = (target: HTMLElement, events: unknown[]) => void | (() => void) | Promise<void | (() => void)>

async function mountRRWebPlayer(target: HTMLElement, events: unknown[]): Promise<() => void> {
  await import('rrweb-player/dist/style.css')
  const { default: RRWebPlayer } = await import('rrweb-player')
  const player = new RRWebPlayer({ target, props: { events: events as never[] } })
  return () => {
    (player as unknown as { $destroy?: () => void }).$destroy?.()
    target.replaceChildren()
  }
}

export function ReplayPlayer({
  load,
  mountPlayer = mountRRWebPlayer,
}: {
  load: (signal: AbortSignal) => Promise<ReplayData>
  mountPlayer?: MountPlayer
}) {
  const target = useRef<HTMLDivElement>(null)
  const [state, setState] = useState<{ loading: boolean; error?: string; truncated: boolean }>({ loading: true, truncated: false })

  useEffect(() => {
    const controller = new AbortController()
    let disposed = false
    let destroy: void | (() => void)
    void load(controller.signal)
      .then(async (data) => {
        if (disposed || !target.current) return
        destroy = await mountPlayer(target.current, data.events)
        if (!disposed) setState({ loading: false, truncated: data.truncated })
      })
      .catch((error: unknown) => {
        if (!disposed && !(error instanceof DOMException && error.name === 'AbortError')) {
          setState({ loading: false, truncated: false, error: error instanceof Error ? error.message : 'Could not load replay.' })
        }
      })
    return () => {
      disposed = true
      controller.abort()
      destroy?.()
    }
  }, [load, mountPlayer])

  return (
    <div className="flex min-w-0 flex-col gap-4">
      {state.loading ? <Skeleton aria-label="Loading replay" className="h-96 w-full" /> : null}
      {state.error ? <Card role="alert"><CardHeader><CardTitle>Replay unavailable</CardTitle></CardHeader><CardContent>{state.error}</CardContent></Card> : null}
      {state.truncated ? <Card role="status"><CardHeader><CardTitle>Recording truncated</CardTitle></CardHeader><CardContent>The replay ends at the last complete event.</CardContent></Card> : null}
      <div ref={target} className="min-h-0 overflow-auto rounded-xl border bg-card" />
    </div>
  )
}
