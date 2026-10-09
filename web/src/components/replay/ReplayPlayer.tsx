import { useEffect, useRef, useState } from 'react'

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import type { ReplayData } from '@/lib/replay-api'

import { rehydrateCSS } from './css-rehydrate'

export type ReplayPlayerHandle = {
  goto: (milliseconds: number) => void
  destroy?: () => void
}

export type MountPlayer = (
  target: HTMLElement,
  events: unknown[],
) => void | (() => void) | ReplayPlayerHandle | Promise<void | (() => void) | ReplayPlayerHandle>

async function mountRRWebPlayer(target: HTMLElement, events: unknown[]): Promise<ReplayPlayerHandle> {
  await import('rrweb-player/dist/style.css')
  const { default: RRWebPlayer } = await import('rrweb-player')
  // Fit the card: rrweb-player defaults to 1024px wide and overflows narrower
  // layouts. 80px leaves room for its controller below the frame.
  const width = Math.max(320, target.clientWidth)
  const height = Math.max(240, Math.round(width * 0.5625) - 80)
  const player = new RRWebPlayer({ target, props: { events: events as never[], width, height } })
  const instance = player as unknown as { $destroy?: () => void; goto: (milliseconds: number) => void }
  return {
    goto: (milliseconds) => instance.goto(milliseconds),
    destroy: () => {
      instance.$destroy?.()
      target.replaceChildren()
    },
  }
}

export function ReplayPlayer({
  load,
  fetchCSS,
  mountPlayer = mountRRWebPlayer,
  onReady,
}: {
  load: (signal: AbortSignal) => Promise<ReplayData>
  fetchCSS?: (hash: string) => Promise<string>
  mountPlayer?: MountPlayer
  onReady?: (player: ReplayPlayerHandle | null) => void
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
        const events = fetchCSS ? await rehydrateCSS(data.events, fetchCSS) : data.events
        if (disposed || !target.current) return
        const mounted = await mountPlayer(target.current, events)
        if (typeof mounted === 'function') {
          destroy = mounted
        } else if (mounted) {
          destroy = mounted.destroy
          onReady?.(mounted)
        }
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
      onReady?.(null)
      destroy?.()
    }
  }, [fetchCSS, load, mountPlayer, onReady])

  return (
    <div className="flex min-w-0 flex-col gap-4">
      {state.loading ? <Skeleton aria-label="Loading replay" className="h-96 w-full" /> : null}
      {state.error ? <Card role="alert"><CardHeader><CardTitle>Replay unavailable</CardTitle></CardHeader><CardContent>{state.error}</CardContent></Card> : null}
      {state.truncated ? <Card role="status"><CardHeader><CardTitle>Recording truncated</CardTitle></CardHeader><CardContent>The replay ends at the last complete event.</CardContent></Card> : null}
      <div ref={target} className="min-h-0 w-full overflow-auto rounded-xl border bg-card" />
    </div>
  )
}
