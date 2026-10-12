import { Link } from '@tanstack/react-router'
import { PlusIcon } from 'lucide-react'

import { SiteIcon } from '@/components/SiteIcon'
import type { Membership } from '@/lib/me'
import { cn } from '@/lib/utils'

/**
 * Compact browser-style tabs for switching between the projects of an organisation.
 * The active tab sits flush with the header below it.
 */
export function ProjectTabs({
  membership,
  activeSiteId,
}: {
  membership: Membership
  activeSiteId?: number
}) {
  const org = membership.org.slug
  const canAdd = membership.role !== 'viewer'

  return (
    <nav
      aria-label="Projects"
      className="sticky top-0 z-30 flex h-9 items-end gap-0.5 overflow-x-auto border-b bg-muted/40 px-2 pt-1.5 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
    >
      {membership.sites.map((item) => {
        const active = item.id === activeSiteId
        return (
          <Link
            key={item.id}
            aria-current={active ? 'page' : undefined}
            className={cn(
              'group relative flex h-[1.875rem] max-w-44 min-w-0 shrink-0 items-center gap-1.5 rounded-t-lg border border-b-0 px-2.5 text-xs font-medium outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring',
              active
                ? '-mb-px border-border bg-background text-foreground'
                : 'border-transparent text-muted-foreground hover:bg-background/60 hover:text-foreground',
            )}
            params={{ org, site: item.slug ?? item.public_id ?? String(item.id) }}
            search={{ period: '30d', compare: 'off', f: [], metric: 'visitors', include_imported: true }}
            title={item.name}
            to="/app/$org/$site"
          >
            <SiteIcon iconUrl={item.icon_url} name={item.name} size={14} />
            <span className="truncate">{item.name}</span>
          </Link>
        )
      })}
      {canAdd ? (
        <Link
          aria-label="Add project"
          className="mb-0.5 ml-0.5 grid size-6 shrink-0 place-items-center rounded-md text-muted-foreground outline-none transition-colors hover:bg-background/60 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
          params={{ org }}
          title="Add project"
          to="/app/$org/sites/new"
        >
          <PlusIcon className="size-3.5" />
        </Link>
      ) : null}
    </nav>
  )
}
