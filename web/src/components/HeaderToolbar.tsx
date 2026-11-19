import { createContext, useContext, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

import { useMinWidth } from '@/hooks/use-min-width'

/** Wide enough for the breadcrumbs, a page's controls and the search field on one 48px bar. */
export const HEADER_TOOLBAR_MIN_WIDTH = 1024

type HeaderToolbarSlot = {
  /** The element in the app header that page controls are portalled into; null outside the shell. */
  node: HTMLElement | null
  /** The last breadcrumb, so a page title that only repeats it can leave the page. */
  pageLabel?: string
}

export const HeaderToolbarContext = createContext<HeaderToolbarSlot>({ node: null })

/**
 * The header slot when page controls belong in the app header: inside the shell and on a wide screen.
 * Narrow screens and pages rendered outside the shell (tests, shared dashboards) keep their controls inline.
 */
export function useHeaderToolbar(): HeaderToolbarSlot & { active: boolean } {
  const slot = useContext(HeaderToolbarContext)
  const wide = useMinWidth(HEADER_TOOLBAR_MIN_WIDTH)
  return { ...slot, active: wide && slot.node !== null }
}

/**
 * Renders a page's filters and actions in the app header, so the page spends no height on a toolbar row.
 * Returns `fallback` (the inline placement) when the header slot is not active.
 */
export function HeaderToolbar({ children, fallback = null }: { children: ReactNode; fallback?: ReactNode }) {
  const { node, active } = useHeaderToolbar()
  if (!active || !node) return fallback
  return createPortal(children, node)
}
