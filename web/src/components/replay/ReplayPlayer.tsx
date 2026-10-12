import { useEffect, useRef, useState } from 'react'

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import type { ReplayData } from '@/lib/replay-api'

import { rehydrateCSS } from './css-rehydrate'
import { fitReplayViewport, type ReplaySize } from './replay-fit'

import './ReplayPlayer.css'

const controllerHeight = 80

export type ReplayPlayerHandle = {
  goto: (milliseconds: number) => void
  destroy?: () => void
  resize?: (stage: ReplaySize) => void
}

export type MountPlayer = (
  target: HTMLElement,
  events: unknown[],
) => void | (() => void) | ReplayPlayerHandle | Promise<void | (() => void) | ReplayPlayerHandle>

async function mountRRWebPlayer(target: HTMLElement, events: unknown[]): Promise<ReplayPlayerHandle> {
  await import('rrweb-player/dist/style.css')
  const { default: RRWebPlayer } = await import('rrweb-player')
  let stage = readElementSize(target)
  let viewport = initialViewport(events)
  const frameHeight = Math.max(1, stage.height - controllerHeight)
  const player = new RRWebPlayer({
    target,
    props: {
      events: events as never[],
      width: Math.max(1, stage.width),
      height: frameHeight,
      maxScale: 0,
    },
  })
  const instance = player as unknown as {
    $destroy?: () => void
    $set: (props: { width: number; height: number; maxScale: number }) => void
    addEventListener: (event: string, handler: (size: unknown) => void) => void
    goto: (milliseconds: number) => void
    triggerResize: () => void
  }

  const fit = () => {
    const height = Math.max(1, stage.height - controllerHeight)
    instance.$set({ width: Math.max(1, stage.width), height, maxScale: 0 })
    instance.triggerResize()
    const wrapper = target.querySelector<HTMLElement>('.replayer-wrapper')
    if (!wrapper || !viewport) return
    const result = fitReplayViewport(viewport, { width: stage.width, height })
    wrapper.style.transform = `scale(${result.scale}) translate(-50%, -50%)`
    target.dataset.replayScale = String(result.scale)
    target.dataset.replayViewportWidth = String(viewport.width)
    target.dataset.replayViewportHeight = String(viewport.height)
  }

  instance.addEventListener('resize', (next) => {
    const size = replaySize(next)
    if (!size) return
    viewport = size
    fit()
  })
  fit()

  return {
    goto: (milliseconds) => instance.goto(milliseconds),
    resize: (nextStage) => {
      stage = nextStage
      fit()
    },
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
        const player = typeof mounted === 'function' || !mounted ? undefined : mounted
        if (typeof mounted === 'function') {
          destroy = mounted
        } else if (player) {
          destroy = player.destroy
          const element = target.current
          const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver((entries) => {
            const entry = entries[0]
            if (entry) player.resize?.({ width: entry.contentRect.width, height: entry.contentRect.height })
          })
          observer?.observe(element)
          player.resize?.(readElementSize(element))
          const destroyPlayer = destroy
          destroy = () => {
            observer?.disconnect()
            destroyPlayer?.()
          }
          onReady?.(player)
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
    <div className="flex h-full min-h-0 min-w-0 flex-1 flex-col gap-4">
      {state.loading ? <Skeleton aria-label="Loading replay" className="h-96 w-full" /> : null}
      {state.error ? <Card role="alert"><CardHeader><CardTitle>Replay unavailable</CardTitle></CardHeader><CardContent>{state.error}</CardContent></Card> : null}
      {state.truncated ? <Card role="status"><CardHeader><CardTitle>Recording truncated</CardTitle></CardHeader><CardContent>The replay ends at the last complete event.</CardContent></Card> : null}
      <div
        ref={target}
        data-testid="replay-stage"
        className="replay-player min-h-72 w-full flex-1 overflow-hidden rounded-lg border bg-muted"
      />
    </div>
  )
}

function readElementSize(element: HTMLElement): ReplaySize {
  const rect = element.getBoundingClientRect()
  return { width: rect.width || element.clientWidth, height: rect.height || element.clientHeight }
}

function replaySize(value: unknown): ReplaySize | undefined {
  if (!value || typeof value !== 'object') return undefined
  const { width, height } = value as { width?: unknown; height?: unknown }
  if (typeof width !== 'number' || typeof height !== 'number' || width <= 0 || height <= 0) return undefined
  return { width, height }
}

function initialViewport(events: unknown[]): ReplaySize | undefined {
  for (const event of events) {
    if (!event || typeof event !== 'object') continue
    const candidate = event as { type?: unknown; data?: unknown }
    if (candidate.type !== 4) continue
    const size = replaySize(candidate.data)
    if (size) return size
  }
  return undefined
}
