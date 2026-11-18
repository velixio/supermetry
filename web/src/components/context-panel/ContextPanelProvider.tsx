import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from 'react'
import { useRouterState } from '@tanstack/react-router'

import type { Membership } from '@/lib/me'

import { recordRecent, readRecents, type RecentPage } from './recents'
import { readPanelOpen, WIDE_QUERY, writePanelOpen } from './storage'

export type ContextPanelState = {
  /** False outside the app shell (and in tests that render the header alone): the toggle then renders nothing. */
  available: boolean
  /** Whether the panel is showing: the remembered choice on wide screens, the sheet below. */
  open: boolean
  wide: boolean
  panelId: string
  toggle: () => void
  close: () => void
  toggleRef: RefObject<HTMLButtonElement | null>
  userId: number | string
  recents: RecentPage[]
}

const noop = () => undefined
const Context = createContext<ContextPanelState>({
  available: false, open: false, wide: false, panelId: 'context-panel', toggle: noop, close: noop,
  toggleRef: { current: null }, userId: 0, recents: [],
})

export const useContextPanel = () => useContext(Context)

/** Ctrl or Cmd with the period key. Own listener, like the sidebar filter: AppShell's useShortcuts map is a single shared one. */
export function isPanelShortcut(event: Pick<KeyboardEvent, 'key' | 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey'>): boolean {
  return (event.ctrlKey || event.metaKey) && !event.altKey && !event.shiftKey && event.key === '.'
}

function useIsWide(): boolean {
  const query = () => (typeof window.matchMedia === 'function' ? window.matchMedia(WIDE_QUERY).matches : window.innerWidth >= 1280)
  const [wide, setWide] = useState(query)
  useEffect(() => {
    const update = () => setWide(query())
    if (typeof window.matchMedia === 'function') {
      const list = window.matchMedia(WIDE_QUERY)
      list.addEventListener('change', update)
      update()
      return () => list.removeEventListener('change', update)
    }
    window.addEventListener('resize', update)
    return () => window.removeEventListener('resize', update)
  }, [])
  return wide
}

export function ContextPanelProvider({ userId, membership, children }: { userId: number | string; membership?: Membership; children: ReactNode }) {
  const wide = useIsWide()
  const [stored, setStored] = useState(() => readPanelOpen(userId))
  const [sheetOpen, setSheetOpen] = useState(false)
  const toggleRef = useRef<HTMLButtonElement>(null)
  const { pathname, searchStr } = useRouterState({ select: (state) => ({ pathname: state.location.pathname, searchStr: state.location.searchStr ?? '' }) })
  const [recents, setRecents] = useState(() => readRecents(userId))
  const searchRef = useRef(searchStr)
  searchRef.current = searchStr

  // Record a visit when the page changes (a new search on the same page is not a new visit).
  useEffect(() => {
    setRecents(recordRecent(userId, pathname, searchRef.current, membership))
    setSheetOpen(false)
  }, [userId, pathname, membership])

  const toggle = useCallback(() => {
    if (wide) {
      setStored((open) => {
        writePanelOpen(userId, !open)
        return !open
      })
    } else setSheetOpen((open) => !open)
  }, [wide, userId])
  const close = useCallback(() => (wide ? setStored((open) => { if (open) writePanelOpen(userId, false); return false }) : setSheetOpen(false)), [wide, userId])

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!isPanelShortcut(event)) return
      event.preventDefault()
      toggle()
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [toggle])

  const value = useMemo<ContextPanelState>(
    () => ({ available: true, open: wide ? stored : sheetOpen, wide, panelId: 'context-panel', toggle, close, toggleRef, userId, recents }),
    [wide, stored, sheetOpen, toggle, close, userId, recents],
  )
  return <Context.Provider value={value}>{children}</Context.Provider>
}
