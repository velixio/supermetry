import { SearchIcon } from 'lucide-react'
import type { KeyboardEvent, RefObject } from 'react'

import { Button } from '@/components/ui/button'
import { SidebarMenu, SidebarMenuButton, SidebarMenuItem } from '@/components/ui/sidebar'
import { ui } from '@/messages/ui'

import { rowClass, type MenuGroupData, type MenuRow } from './menu'

/** The key that focuses the menu filter. `/` is unbound elsewhere (see lib/shortcuts and AppShell). */
export const FILTER_SHORTCUT = '/'

const includes = (text: string, q: string) => text.toLowerCase().includes(q)

/**
 * Keeps the entries that match the query (case-insensitive). A group name that matches keeps the whole group;
 * an entry that matches keeps all its sub-items; otherwise only the matching sub-items stay under their parent.
 * Groups left empty drop out.
 */
export function filterGroups(groups: MenuGroupData[], query: string): MenuGroupData[] {
  const q = query.trim().toLowerCase()
  if (!q) return groups
  return groups.flatMap((group) => {
    const groupHit = includes(group.label, q)
    const entries = group.entries.flatMap((entry): MenuRow[] => {
      if (groupHit || includes(entry.label, q)) return [entry]
      const kids = entry.children?.filter((child) => includes(child.label, q)) ?? []
      return kids.length > 0 ? [{ ...entry, children: kids }] : []
    })
    return entries.length > 0 ? [{ ...group, entries }] : []
  })
}

/** Rows in visual order, each entry followed by its visible sub-items: the list the arrow keys walk. */
export function flattenRows(groups: MenuGroupData[]): MenuRow[] {
  return groups.flatMap((group) => group.entries.flatMap((entry) => [entry, ...(entry.children ?? [])]))
}

/** The search field at the top of the nav, or on the icon rail a button that expands the sidebar and focuses it. */
export function SidebarFilterField({
  rail,
  query,
  onQueryChange,
  onKeyDown,
  onExpand,
  inputRef,
  activeId,
  listId,
  count,
}: {
  rail: boolean
  query: string
  onQueryChange: (value: string) => void
  onKeyDown: (event: KeyboardEvent<HTMLInputElement>) => void
  onExpand: () => void
  inputRef: RefObject<HTMLInputElement | null>
  /** DOM id of the row the arrow keys are on, while a query is typed. */
  activeId?: string
  listId: string
  count: number
}) {
  if (rail) {
    return (
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton aria-keyshortcuts={FILTER_SHORTCUT} className={rowClass} onClick={onExpand} tooltip={ui.sidebar.filterLabel} type="button">
            <SearchIcon aria-hidden="true" />
            <span>{ui.sidebar.filterLabel}</span>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    )
  }
  return (
    <div className="relative">
      <SearchIcon aria-hidden="true" className="pointer-events-none absolute top-1/2 left-2 size-4 -translate-y-1/2 text-muted-foreground" />
      <input
        ref={inputRef}
        aria-activedescendant={query.trim() && count > 0 ? activeId : undefined}
        aria-controls={listId}
        aria-keyshortcuts={FILTER_SHORTCUT}
        aria-label={ui.sidebar.filterLabel}
        autoComplete="off"
        className="h-11 w-full rounded-md bg-muted/60 pr-7 pl-8 text-[13px] text-foreground outline-hidden placeholder:text-muted-foreground focus-visible:ring-2 focus-visible:ring-ring/50 md:h-8"
        onChange={(event) => onQueryChange(event.target.value)}
        onKeyDown={onKeyDown}
        placeholder={ui.sidebar.filterLabel}
        role="searchbox"
        spellCheck={false}
        type="text"
        value={query}
      />
      {query === '' ? (
        <kbd aria-hidden="true" className="pointer-events-none absolute top-1/2 right-2 hidden h-4 min-w-4 -translate-y-1/2 items-center justify-center rounded-sm bg-background/80 px-1 text-[10px] font-medium text-muted-foreground md:inline-flex">
          {FILTER_SHORTCUT}
        </kbd>
      ) : null}
    </div>
  )
}

/** Shown in place of the groups when nothing matches. */
export function NoMatches({ onSearchEverything }: { onSearchEverything: () => void }) {
  return (
    <div className="flex flex-col items-start gap-1 px-2 py-1 text-[13px] text-muted-foreground group-data-[collapsible=icon]:hidden">
      <p>{ui.sidebar.noMatches}</p>
      <Button className="h-7 px-2 text-[13px] text-muted-foreground" onClick={onSearchEverything} size="sm" type="button" variant="ghost">
        {ui.sidebar.searchEverything}
      </Button>
    </div>
  )
}
