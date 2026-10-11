import { useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

import { cn } from '@/lib/utils'

export type TooltipRect = { left: number; right: number; top: number; bottom: number }
type TooltipSize = { width: number; height: number }
type ViewportSize = { width: number; height: number }

const VIEWPORT_MARGIN = 8
const TOOLTIP_GAP = 8

function overlaps(left: number, top: number, size: TooltipSize, rect: TooltipRect): boolean {
  return left < rect.right && left + size.width > rect.left && top < rect.bottom && top + size.height > rect.top
}

export function placeTooltip(
  anchor: TooltipRect,
  size: TooltipSize,
  viewport: ViewportSize,
  avoid?: TooltipRect,
): { left: number; top: number; placement: 'above' | 'below'; arrowLeft: number } {
  const anchorCenter = (anchor.left + anchor.right) / 2
  const maxLeft = Math.max(VIEWPORT_MARGIN, viewport.width - size.width - VIEWPORT_MARGIN)
  const left = Math.min(Math.max(anchorCenter - size.width / 2, VIEWPORT_MARGIN), maxLeft)
  let top = anchor.top - size.height - TOOLTIP_GAP

  if (avoid && overlaps(left, top, size, avoid)) top = avoid.top - size.height - TOOLTIP_GAP
  let placement: 'above' | 'below' = 'above'
  if (top < VIEWPORT_MARGIN) {
    top = Math.min(anchor.bottom + TOOLTIP_GAP, Math.max(VIEWPORT_MARGIN, viewport.height - size.height - VIEWPORT_MARGIN))
    placement = 'below'
  }

  return {
    left,
    top,
    placement,
    arrowLeft: Math.min(Math.max(anchorCenter - left, 10), Math.max(10, size.width - 10)),
  }
}

export function rectFromElement(element: Element): TooltipRect {
  const rect = element.getBoundingClientRect()
  return { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom }
}

export function rectFromPoint(x: number, y: number): TooltipRect {
  return { left: x, right: x, top: y, bottom: y }
}

export function ChartTooltip({ anchor, avoid, children, className }: {
  anchor?: TooltipRect
  avoid?: TooltipRect
  children: ReactNode
  className?: string
}) {
  const tooltipRef = useRef<HTMLDivElement>(null)
  const [position, setPosition] = useState<ReturnType<typeof placeTooltip>>()

  useLayoutEffect(() => {
    const tooltip = tooltipRef.current
    if (!tooltip || !anchor) return
    const measure = () => setPosition(placeTooltip(
      anchor,
      { width: tooltip.offsetWidth, height: tooltip.offsetHeight },
      { width: window.innerWidth, height: window.innerHeight },
      avoid,
    ))
    measure()
    window.addEventListener('resize', measure)
    return () => window.removeEventListener('resize', measure)
  }, [anchor, avoid, children])

  if (!anchor || typeof document === 'undefined') return null
  return createPortal(
    <div
      ref={tooltipRef}
      className={cn('pointer-events-none fixed z-50 rounded-md bg-popover px-2.5 py-1.5 text-xs text-popover-foreground shadow-md ring-1 ring-border', className)}
      role="tooltip"
      style={{ left: position?.left ?? 0, top: position?.top ?? 0, visibility: position ? 'visible' : 'hidden' }}
    >
      {children}
      {position ? <span
        aria-hidden="true"
        className={cn(
          'absolute size-2 -translate-x-1/2 rotate-45 bg-popover ring-border',
          position.placement === 'above' ? '-bottom-1 border-b border-r' : '-top-1 border-l border-t',
        )}
        style={{ left: position.arrowLeft }}
      /> : null}
    </div>,
    document.body,
  )
}
