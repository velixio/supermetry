import { ChevronDownIcon, ChevronRightIcon, PinIcon, PinOffIcon } from 'lucide-react'
import type { ReactNode } from 'react'

import {
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuAction,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubItem,
} from '@/components/ui/sidebar'
import { ui } from '@/messages/ui'

/**
 * Matte, dense row styling. Desktop rows are 28px tall; below md they keep the 44px touch target.
 * Selection is a plain bg-accent fill (no border or bar); hover is a lighter fill; `data-kbd` marks the
 * row the menu filter's arrow keys are on.
 */
export const rowClass =
  'h-11 gap-2 rounded-md px-2 text-[13px] md:h-7 hover:bg-accent/60 hover:text-foreground active:bg-accent active:text-foreground data-[active=true]:bg-accent data-[active=true]:font-medium data-[active=true]:text-foreground data-[kbd=true]:bg-accent [&>svg]:size-4'

export const subRowClass =
  'h-11 gap-2 rounded-md px-2 text-[13px] text-sidebar-foreground md:h-7 hover:bg-accent/60 hover:text-foreground active:bg-accent active:text-foreground data-[active=true]:bg-accent data-[active=true]:font-medium data-[active=true]:text-foreground data-[kbd=true]:bg-accent'

/** What a row needs to draw itself: the (possibly highlighted) label, a DOM id and whether the filter cursor is on it. */
export type RowContext = { label: ReactNode; id: string; kbd: boolean }

export type MenuRow = {
  key: string
  label: string
  /** Where Enter takes the user from the menu filter. */
  href: string
  active: boolean
  render: (context: RowContext) => ReactNode
  pinnable?: boolean
  children?: MenuRow[]
}

export type MenuGroupData = { key: string; label: string; entries: MenuRow[]; className?: string }

export const optionId = (key: string) => `sidebar-opt-${key.replace(/[^\w-]/g, '_')}`

/** Bold the first case-insensitive occurrence of the query inside a label. */
export function Highlight({ label, query }: { label: string; query: string }) {
  const q = query.trim().toLowerCase()
  const at = q ? label.toLowerCase().indexOf(q) : -1
  if (at < 0) return <>{label}</>
  return (
    <>
      {label.slice(0, at)}
      <span className="font-semibold text-foreground" data-match="">{label.slice(at, at + q.length)}</span>
      {label.slice(at + q.length)}
    </>
  )
}

/** One top-level row, with its collapsible second level when it has sub-destinations. */
export function MenuEntry({
  entry,
  query,
  open,
  onToggle,
  cursor,
  pin,
}: {
  entry: MenuRow
  query: string
  /** Whether the second level is shown (already forced open while filtering). */
  open: boolean
  onToggle: () => void
  cursor: string | null
  pin?: { pinned: boolean; toggle: () => void }
}) {
  const filtering = query.trim() !== ''
  const kids = entry.children ?? []
  const subId = `sidebar-sub-${entry.key.replace(/[^\w-]/g, '_')}`
  const row = (item: MenuRow) => item.render({ label: <Highlight label={item.label} query={query} />, id: optionId(item.key), kbd: cursor === item.key })
  return (
    <SidebarMenuItem>
      {row(entry)}
      {kids.length > 0 && !filtering ? (
        <SidebarMenuAction
          aria-controls={subId}
          aria-expanded={open}
          aria-label={open ? ui.sidebar.collapse(entry.label) : ui.sidebar.expand(entry.label)}
          className="text-muted-foreground max-md:peer-data-[size=default]/menu-button:top-3 md:peer-data-[size=default]/menu-button:top-1"
          onClick={onToggle}
          type="button"
        >
          <ChevronRightIcon aria-hidden="true" className={`transition-transform motion-reduce:transition-none ${open ? 'rotate-90' : ''}`} />
        </SidebarMenuAction>
      ) : pin ? (
        <SidebarMenuAction
          aria-label={pin.pinned ? ui.sidebar.unpin(entry.label) : ui.sidebar.pin(entry.label)}
          aria-pressed={pin.pinned}
          className="max-md:peer-data-[size=default]/menu-button:top-3 md:peer-data-[size=default]/menu-button:top-1"
          showOnHover={!pin.pinned}
          onClick={pin.toggle}
          type="button"
        >
          {pin.pinned ? <PinOffIcon aria-hidden="true" /> : <PinIcon aria-hidden="true" />}
        </SidebarMenuAction>
      ) : null}
      {kids.length > 0 && open ? (
        <SidebarMenuSub className="mx-0 ml-4 gap-0.5 border-sidebar-border py-0.5 pr-0 pl-2" id={subId}>
          {kids.map((child) => (
            <SidebarMenuSubItem key={child.key}>{row(child)}</SidebarMenuSubItem>
          ))}
        </SidebarMenuSub>
      ) : null}
    </SidebarMenuItem>
  )
}

/** A labelled group whose heading collapses it; the chevron shows on hover or focus, and always while collapsed. */
export function MenuGroup({
  id,
  label,
  open,
  onToggle,
  rail,
  className,
  fixed = false,
  children,
}: {
  id: string
  label: string
  open: boolean
  onToggle: () => void
  rail: boolean
  className?: string
  /** A heading that does not collapse (a group with one or two rows). */
  fixed?: boolean
  children: ReactNode
}) {
  const headingId = `sidebar-${id}-heading`
  const contentId = `sidebar-${id}-content`
  return (
    <SidebarGroup className={`p-0 ${className ?? ''}`}>
      <SidebarGroupLabel asChild className="group/label h-6 px-2 text-xs font-medium text-muted-foreground group-data-[collapsible=icon]:-mt-6">
        <h2 id={headingId}>
          {fixed ? <span>{label}</span> : <button
            aria-controls={contentId}
            aria-expanded={open}
            className="flex h-full w-full items-center justify-between rounded-md text-left outline-hidden focus-visible:ring-2 focus-visible:ring-ring/50"
            onClick={onToggle}
            tabIndex={rail ? -1 : undefined}
            type="button"
          >
            <span>{label}</span>
            <ChevronDownIcon
              aria-hidden="true"
              className={`size-3.5 transition-[transform,opacity] group-focus-within/label:opacity-100 group-hover/label:opacity-100 max-md:opacity-100 motion-reduce:transition-none ${open ? 'opacity-0' : '-rotate-90 opacity-100'}`}
            />
          </button>}
        </h2>
      </SidebarGroupLabel>
      <SidebarGroupContent hidden={!open && !fixed} id={contentId}>
        <nav aria-labelledby={headingId}>
          <SidebarMenu className="gap-0.5">{children}</SidebarMenu>
        </nav>
      </SidebarGroupContent>
    </SidebarGroup>
  )
}
