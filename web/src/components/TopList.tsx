import type { ReactNode } from 'react'
import { CompassIcon, ExternalLinkIcon, GlobeIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { breakdownNote } from '@/lib/breakdown-labels'
import { cn } from '@/lib/utils'
import { dashboardView } from '@/messages/dashboardView'

/** The share bar behind a row's label. Every top-N panel draws it the same way. */
export function TopListBar({ width }: { width: string }) {
  return <span aria-hidden="true" className="absolute inset-y-1 left-0 min-w-1.5 rounded-sm bg-chart-1/15 group-hover/row:bg-chart-1/25" data-slot="top-list-bar" style={{ width }} />
}

export function barWidth(value: number, maximum: number): string {
  return maximum > 0 ? `${Math.max(2, (value / maximum) * 100)}%` : '0%'
}

/** Row class shared by the list and the table layouts: hover and keyboard focus read the same. */
export const topListRowClass = 'group/row relative rounded-md hover:bg-muted/50 focus-within:bg-muted/50'

/**
 * Actions at the end of a row. Revealed on hover and keyboard focus (always shown where there is no hover),
 * and the slot is reserved on every row so the numbers stay aligned.
 */
export function TopListActions({ children, reserve }: { children?: ReactNode; reserve: boolean }) {
  if (!reserve) return null
  return (
    <span
      className="flex h-8 w-16 shrink-0 items-center justify-end gap-0.5 opacity-0 transition-opacity duration-150 group-hover/row:opacity-100 group-focus-within/row:opacity-100 [@media(hover:none)]:opacity-100 motion-reduce:transition-none"
      data-slot="top-list-actions"
    >
      {children}
    </span>
  )
}

export type RowLink = { url: string; target: string }

const hostLike = /^(?:[a-z0-9-]+\.)+[a-z]{2,}$/i

/** Where a row's value points, when it points anywhere: a page of the site, or a referring site. */
export function rowLink(dimension: string, value: string, siteHost?: string): RowLink | undefined {
  if (value === '') return undefined
  if (dimension === 'page' || dimension === 'entry' || dimension === 'exit') {
    if (!siteHost || !value.startsWith('/')) return undefined
    return { url: `https://${siteHost}${value}`, target: value }
  }
  if ((dimension === 'source' || dimension === 'referrer') && hostLike.test(value)) return { url: `https://${value}`, target: value }
  return undefined
}

/**
 * The host page paths open on: the first concrete allowed host, else the base
 * domain of a `*.example.com` pattern (example.com).
 */
export function primaryHost(allowedHosts?: string[]): string | undefined {
  const concrete = allowedHosts?.find((host) => host !== '' && !host.includes('*'))
  if (concrete) return concrete
  const wildcard = allowedHosts?.find((host) => host.startsWith('*.') && !host.slice(2).includes('*'))
  return wildcard ? wildcard.slice(2) : undefined
}

export function OpenLinkAction({ link }: { link: RowLink }) {
  const label = dashboardView.openInNewTab(link.target)
  return (
    <Button asChild className="text-muted-foreground" size="icon-sm" variant="ghost">
      <a aria-label={label} href={link.url} rel="noopener noreferrer" target="_blank" title={label}>
        <ExternalLinkIcon aria-hidden="true" />
      </a>
    </Button>
  )
}

/** Leading icon for source rows; a no-referrer row gets its own, so no row is left without one. */
export function SourceIcon({ value }: { value: string }) {
  const Icon = value === '' ? CompassIcon : GlobeIcon
  return <Icon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" data-testid="source-icon" />
}

/** A row label that explains itself where the raw value does not (no-referrer traffic). */
export function RowLabel({ dimension, value, display, className }: { dimension: string; value: string; display: string; className?: string }) {
  const note = breakdownNote(dimension, value)
  if (!note) return <span className={cn('min-w-0 truncate', className)} title={display}>{display}</span>
  return (
    <span className={cn('min-w-0 truncate', className)} title={note} tabIndex={0}>
      {display}
      <span className="sr-only">. {note}</span>
    </span>
  )
}
