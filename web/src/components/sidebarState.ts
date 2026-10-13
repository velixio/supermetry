/** From this width the desktop sidebar starts expanded; between the mobile sheet (768px) and here it starts on the icon rail. */
export const SIDEBAR_EXPANDED_MIN_WIDTH = 1280

/** The saved `sidebar_state` cookie wins; without one, wide screens start expanded and tablets start on the rail. */
export function sidebarStartsOpen(cookie: string, width: number): boolean {
  const saved = cookie.split('; ').find((entry) => entry.startsWith('sidebar_state='))
  if (saved === 'sidebar_state=true') return true
  if (saved === 'sidebar_state=false') return false
  return width >= SIDEBAR_EXPANDED_MIN_WIDTH
}
