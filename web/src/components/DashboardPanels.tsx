import { XIcon } from 'lucide-react'
import { useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode, type RefObject } from 'react'

import { Button } from '@/components/ui/button'
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from '@/components/ui/resizable'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { useIsMobile } from '@/hooks/use-mobile'
import { useMinWidth } from '@/hooks/use-min-width'
import { ui } from '@/messages/ui'

import { INSPECTOR_WIDTH, readInspectorWidth, remFromPixels, writeInspectorWidth } from './inspectorWidth'
import { SIDEBAR_EXPANDED_MIN_WIDTH } from './sidebarState'

export type InspectorPanel = {
  /** Identifies the inspected value; focus moves to the heading whenever it changes. */
  key: string
  title: string
  dimension: string
  body: ReactNode
  onClose(): void
}

type DashboardPanelsProps = {
  main: ReactNode
  inspector?: InspectorPanel
  /** The control that opened the inspector; focus returns to it on close. */
  returnFocusRef: RefObject<HTMLElement | null>
}

// Spec 11: main keeps at least 40rem beside the inspector. The side layout needs
// MAIN_MIN_REM + HANDLE_REM (handle plus its mx-2 gutters) + the inspector's minimum width
// (a user-resized width is always >= that minimum), measured on the container, not the viewport.
const MAIN_MIN_REM = 40
const HANDLE_REM = 1.25
const SIDE_LAYOUT_MIN_REM = MAIN_MIN_REM + HANDLE_REM + INSPECTOR_WIDTH.min

/** Width in px of the observed element's content box; undefined until the first measurement. */
function useContainerWidth(ref: RefObject<HTMLElement | null>): number | undefined {
  const [width, setWidth] = useState<number | undefined>(undefined)
  // Measure before the first paint so the first frame already uses the right layout.
  useLayoutEffect(() => {
    const measured = ref.current?.getBoundingClientRect().width
    if (measured) setWidth(measured)
  }, [ref])
  useEffect(() => {
    const element = ref.current
    if (!element || typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver((entries) => {
      const entry = entries[entries.length - 1]
      if (entry) setWidth(entry.contentRect.width)
    })
    observer.observe(element)
    return () => observer.disconnect()
  }, [ref])
  return width
}

export function DashboardPanels({ main, inspector, returnFocusRef }: DashboardPanelsProps) {
  const mobile = useIsMobile()
  const wrapperRef = useRef<HTMLDivElement>(null)
  const containerWidth = useContainerWidth(wrapperRef)
  const containerFits = containerWidth === undefined || remFromPixels(containerWidth) >= SIDE_LAYOUT_MIN_REM
  const wide = useMinWidth(SIDEBAR_EXPANDED_MIN_WIDTH) && !mobile && containerFits
  const headingId = useId()
  const headingRef = useRef<HTMLHeadingElement>(null)
  const [width] = useState(readInspectorWidth)
  const wasOpen = useRef(false)
  // The sheet animates out after the selection is cleared; keep its last content for that frame.
  const lastInspector = useRef(inspector)
  if (inspector) lastInspector.current = inspector
  const key = inspector?.key

  useEffect(() => {
    if (!wide) {
      wasOpen.current = false
      return
    }
    if (key !== undefined) {
      wasOpen.current = true
      headingRef.current?.focus()
      return
    }
    if (!wasOpen.current) return
    wasOpen.current = false
    const origin = returnFocusRef.current
    if (origin?.isConnected) origin.focus()
  }, [key, wide, returnFocusRef])

  const closeOnEscape = (event: KeyboardEvent<HTMLElement>) => {
    // A dialog opened from the inspector (full report) handles its own Esc first and marks it handled.
    if (event.key !== 'Escape' || event.defaultPrevented) return
    event.preventDefault()
    inspector?.onClose()
  }

  const shown = inspector ?? lastInspector.current
  // `main` stays at one tree position so a layout change never remounts it.
  return (
    <div ref={wrapperRef} className="min-w-0">
      <ResizablePanelGroup className="items-stretch" orientation="horizontal" style={{ height: 'auto', overflow: 'visible' }}>
        <ResizablePanel id="dashboard-main" minSize={wide ? `${MAIN_MIN_REM}rem` : undefined} style={{ overflow: 'visible', maxHeight: 'none' }}>
          <div className="flex min-w-0 flex-col gap-4">{main}</div>
        </ResizablePanel>
        {wide && inspector ? (
          <>
            <ResizableHandle withHandle aria-label={ui.inspector.resize} className="mx-2" />
            <ResizablePanel
              id="dashboard-inspector"
              defaultSize={`${width}rem`}
              groupResizeBehavior="preserve-pixel-size"
              maxSize={`${INSPECTOR_WIDTH.max}rem`}
              minSize={`${INSPECTOR_WIDTH.min}rem`}
              style={{ overflow: 'visible', maxHeight: 'none' }}
              onResize={(size) => {
                if (size.inPixels > 0) writeInspectorWidth(remFromPixels(size.inPixels))
              }}
            >
              <aside
                aria-labelledby={headingId}
                className="sticky top-28 flex max-h-[calc(100dvh-7.5rem)] min-w-0 flex-col overflow-y-auto rounded-xl border bg-card text-card-foreground"
                onKeyDown={closeOnEscape}
              >
                <header className="flex items-start justify-between gap-2 border-b p-4">
                  <div className="flex min-w-0 flex-col gap-0.5">
                    <p className="text-xs text-muted-foreground">{inspector.dimension}</p>
                    <h2 ref={headingRef} className="break-words text-sm font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring" id={headingId} tabIndex={-1}>
                      {inspector.title}
                    </h2>
                  </div>
                  <Button aria-label={ui.inspector.close} size="icon-sm" type="button" variant="ghost" onClick={inspector.onClose}>
                    <XIcon />
                  </Button>
                </header>
                {inspector.body}
              </aside>
            </ResizablePanel>
          </>
        ) : null}
      </ResizablePanelGroup>
      {wide ? null : (
        <Sheet open={inspector !== undefined} onOpenChange={(open) => { if (!open) inspector?.onClose() }}>
          <SheetContent
            className={mobile
              ? 'gap-0 overflow-y-auto p-0 data-[side=bottom]:h-[85dvh]'
              : 'gap-0 overflow-y-auto p-0 data-[side=right]:w-[min(24rem,100vw)]'}
            showCloseButton={false}
            side={mobile ? 'bottom' : 'right'}
            onCloseAutoFocus={(event) => {
              const origin = returnFocusRef.current
              if (!origin?.isConnected) return
              event.preventDefault()
              origin.focus()
            }}
          >
            {shown ? (
              <>
                <SheetHeader className="flex-row items-start justify-between gap-2 border-b">
                  <div className="flex min-w-0 flex-col gap-0.5">
                    <SheetDescription className="text-xs">{shown.dimension}</SheetDescription>
                    <SheetTitle className="break-words">{shown.title}</SheetTitle>
                  </div>
                  <Button aria-label={ui.inspector.close} size="icon-sm" type="button" variant="ghost" onClick={shown.onClose}>
                    <XIcon />
                  </Button>
                </SheetHeader>
                {shown.body}
              </>
            ) : null}
          </SheetContent>
        </Sheet>
      )}
    </div>
  )
}
