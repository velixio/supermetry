/** Per-user context panel preferences in localStorage. Every read and write is guarded: storage can be blocked or full. */
export const WIDE_QUERY = '(min-width: 1280px)'
/** Below this width the panel starts closed; from here up it starts open. */
export const DEFAULT_OPEN_MIN_WIDTH = 1536

const openKey = (userId: number | string) => `sm.contextPanel.open.v1.${userId}`
const sectionsKey = (userId: number | string) => `sm.contextPanel.sections.v1.${userId}`

function read(key: string): unknown {
  try {
    const raw = localStorage.getItem(key)
    return raw === null ? null : JSON.parse(raw)
  } catch {
    return null
  }
}

function write(key: string, value: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(value))
  } catch {
    // The preference then lasts for this page only.
  }
}

export function readPanelOpen(userId: number | string, width: number = typeof window === 'undefined' ? 0 : window.innerWidth): boolean {
  const stored = read(openKey(userId))
  return typeof stored === 'boolean' ? stored : width >= DEFAULT_OPEN_MIN_WIDTH
}

export function writePanelOpen(userId: number | string, open: boolean) {
  write(openKey(userId), open)
}

/** Section ids the user has collapsed. */
export function readCollapsedSections(userId: number | string): string[] {
  const stored = read(sectionsKey(userId))
  return Array.isArray(stored) ? stored.filter((item): item is string => typeof item === 'string') : []
}

export function writeCollapsedSections(userId: number | string, collapsed: string[]) {
  write(sectionsKey(userId), collapsed)
}

export { read as readJson, write as writeJson }
