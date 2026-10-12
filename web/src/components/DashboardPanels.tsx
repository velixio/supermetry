import { XIcon } from 'lucide-react'
import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode, type RefObject } from 'react'

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

export function DashboardPanels({ main, inspector, returnFocusRef }: DashboardPanelsProps) {
  const mobile = useIsMobile()
  const wide = useMinWidth(SIDEBAR_EXPANDED_MIN_WIDTH) && !mobile
  const headingId = useId()
  const headingRef = useRef<HTMLHeadingElement>(null)
  const [width] = useState(readInspectorWidth)
  const wasOpen = useRef(false)
  // The sheet animates out after the selection is cleared; keep its last content for that frame.
  const lastInspector = useRef(inspector)
  if (inspector) lastInspector.current = inspector
  const key = inspector?.key

  useEffect(() => {
    if (!wide) return
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

  if (wide) {
    return (
      <ResizablePanelGroup className="items-stretch" orientation="horizontal" style={{ height: 'auto', overflow: 'visible' }}>
        <ResizablePanel id="dashboard-main" minSize="40rem" style={{ overflow: 'visible', maxHeight: 'none' }}>
          <div className="flex min-w-0 flex-col gap-4">{main}</div>
        </ResizablePanel>
        {inspector ? (
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
    )
  }

  const shown = inspector ?? lastInspector.current
  return (
    <>
      <div className="flex min-w-0 flex-col gap-4">{main}</div>
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
    </>
  )
}
