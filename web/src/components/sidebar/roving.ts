import { useEffect, useRef, type KeyboardEvent } from 'react'

/**
 * The nav is one roving-tabindex list: a single item is in the tab order, the arrow keys walk the visible
 * items (group headings, rows and sub-items; collapsed groups are skipped), and Tab leaves the list.
 * It reads the DOM (`data-nav-item`, `data-nav-group`, the `aria-expanded` toggles), so the same hook drives
 * the project sidebar and the account sidebar, expanded or on the icon rail.
 */
const isRail = (root: HTMLElement) => root.closest('[data-slot="sidebar"]')?.getAttribute('data-collapsible') === 'icon'

function visibleItems(root: HTMLElement): HTMLElement[] {
  const rail = isRail(root)
  return Array.from(root.querySelectorAll<HTMLElement>('[data-nav-item]')).filter((el) => !el.closest('[hidden]') && !(rail && (el.hasAttribute('data-nav-group') || el.closest('[data-slot="sidebar-menu-sub"]'))))
}

/** The control that expands or collapses an item: the heading itself, or the chevron beside a row with sub-items. */
function toggleOf(el: HTMLElement): HTMLElement | null {
  if (el.hasAttribute('data-nav-group')) return el
  if (el.closest('[data-slot="sidebar-menu-sub"]')) return null
  return el.closest('[data-slot="sidebar-menu-item"]')?.querySelector<HTMLElement>(':scope > [data-sidebar="menu-action"][aria-expanded]') ?? null
}

function parentOf(el: HTMLElement): HTMLElement | null {
  if (el.hasAttribute('data-nav-group')) return null
  if (el.closest('[data-slot="sidebar-menu-sub"]')) return el.closest('[data-slot="sidebar-menu-item"]')?.querySelector<HTMLElement>(':scope > [data-nav-item]') ?? null
  return el.closest('[data-slot="sidebar-group"]')?.querySelector<HTMLElement>('[data-nav-group]') ?? null
}

export function useRovingNav({ onRailExpand }: { onRailExpand?: () => void } = {}) {
  const ref = useRef<HTMLDivElement>(null)
  const current = useRef<HTMLElement | null>(null)

  // After every render: exactly one item is tabbable (the last focused one, else the page on screen, else the first).
  useEffect(() => {
    const root = ref.current
    if (!root) return
    const items = visibleItems(root)
    const stay = current.current && items.includes(current.current) ? current.current : (items.find((el) => el.getAttribute('aria-current') === 'page') ?? items[0])
    for (const el of items) el.tabIndex = el === stay ? 0 : -1
  })

  const onFocus = (event: { target: EventTarget }) => {
    const item = (event.target as HTMLElement).closest?.<HTMLElement>('[data-nav-item]')
    if (!item || item === current.current) return
    current.current = item
    const root = ref.current
    if (root) for (const el of visibleItems(root)) el.tabIndex = el === item ? 0 : -1
  }

  const onKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    const root = ref.current
    const el = (event.target as HTMLElement).closest?.<HTMLElement>('[data-nav-item]')
    if (!root || !el || event.ctrlKey || event.metaKey || event.altKey) return
    const items = visibleItems(root)
    const at = items.indexOf(el)
    const go = (target: HTMLElement | undefined | null) => {
      if (!target) return
      event.preventDefault()
      target.focus()
    }
    const toggle = toggleOf(el)
    const expanded = toggle?.getAttribute('aria-expanded') === 'true'
    switch (event.key) {
      case 'ArrowDown': return go(items[at + 1] ?? items[at])
      case 'ArrowUp': return go(items[at - 1] ?? items[at])
      case 'Home': return go(items[0])
      case 'End': return go(items[items.length - 1])
      case 'ArrowRight':
        if (!toggle) return
        event.preventDefault()
        if (isRail(root)) onRailExpand?.()
        if (!expanded) toggle.click()
        else go(items[at + 1])
        return
      case 'ArrowLeft':
        if (toggle && expanded && !isRail(root)) {
          event.preventDefault()
          toggle.click()
          return
        }
        return go(parentOf(el))
      case 'P':
        // Shift+P pins or unpins the row (plain p is the second key of the "g p" chord).
        el.closest('[data-slot="sidebar-menu-item"]')?.querySelector<HTMLElement>(':scope > [data-sidebar="menu-action"][aria-pressed]')?.click()
        return
    }
  }

  return { ref, onKeyDown, onFocus }
}
