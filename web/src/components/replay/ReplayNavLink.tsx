import { Link } from '@tanstack/react-router'
import { PlaySquareIcon } from 'lucide-react'
import type { ComponentProps } from 'react'
import { defaultSearch } from '@/lib/search'

type ReplayNavLinkProps = { enabled: boolean; org: string; site: string } & Omit<
  ComponentProps<'a'>,
  'href' | 'children'
>

// SidebarMenuButton renders this through asChild, so the button's classes and
// data attributes must reach the anchor or the item loses its menu styling.
export function ReplayNavLink({ enabled, org, site, ...props }: ReplayNavLinkProps) {
  if (!enabled) return null
  return (
    <Link
      {...props}
      aria-label="Replays"
      to="/app/$org/$site/replays"
      params={{ org, site }}
      search={defaultSearch()}
    >
      <PlaySquareIcon />
      <span>Replays</span>
    </Link>
  )
}
