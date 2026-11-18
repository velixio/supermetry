import { useCallback, useState } from 'react'

/** Per-user shell preferences kept in localStorage: open sidebar groups, pinned items, recent palette commands. */
export type SidebarPrefs = { open: Record<string, boolean>; pinned: string[] }

const emptyPrefs: SidebarPrefs = { open: {}, pinned: [] }
const sidebarKey = (userId: number | string) => `sm.sidebar.v1.${userId}`
const recentKey = (userId: number | string) => `sm.palette.recent.v1.${userId}`
const RECENT_LIMIT = 5

function read(key: string): unknown {
  try {
    const raw = localStorage.getItem(key)
    return raw ? JSON.parse(raw) : null
  } catch {
    return null
  }
}

function write(key: string, value: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(value))
  } catch {
    // Storage can be blocked or full; the preference then lasts for this page only.
  }
}

export function readSidebarPrefs(userId: number | string): SidebarPrefs {
  const raw = read(sidebarKey(userId)) as Partial<SidebarPrefs> | null
  if (!raw || typeof raw !== 'object') return emptyPrefs
  const open: Record<string, boolean> = {}
  if (raw.open && typeof raw.open === 'object') {
    for (const [key, value] of Object.entries(raw.open)) if (typeof value === 'boolean') open[key] = value
  }
  const pinned = Array.isArray(raw.pinned) ? raw.pinned.filter((item): item is string => typeof item === 'string') : []
  return { open, pinned }
}

export function useSidebarPrefs(userId: number | string) {
  const [prefs, setPrefs] = useState(() => readSidebarPrefs(userId))
  const update = useCallback((change: (current: SidebarPrefs) => SidebarPrefs) => {
    setPrefs((current) => {
      const next = change(current)
      write(sidebarKey(userId), next)
      return next
    })
  }, [userId])
  const setGroupOpen = useCallback((group: string, open: boolean) => update((current) => ({ ...current, open: { ...current.open, [group]: open } })), [update])
  const togglePin = useCallback((item: string) => update((current) => ({
    ...current,
    pinned: current.pinned.includes(item) ? current.pinned.filter((entry) => entry !== item) : [...current.pinned, item],
  })), [update])
  return { prefs, setGroupOpen, togglePin }
}

export function readRecentCommands(userId: number | string): string[] {
  const raw = read(recentKey(userId))
  return Array.isArray(raw) ? raw.filter((item): item is string => typeof item === 'string').slice(0, RECENT_LIMIT) : []
}

export function recordRecentCommand(userId: number | string, id: string) {
  const next = [id, ...readRecentCommands(userId).filter((entry) => entry !== id)].slice(0, RECENT_LIMIT)
  write(recentKey(userId), next)
}
