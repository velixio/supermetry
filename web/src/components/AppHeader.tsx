import { useId } from 'react'
import { Link } from '@tanstack/react-router'
import { CircleHelpIcon, MoonIcon, SunIcon } from 'lucide-react'

import { Breadcrumbs } from './Breadcrumbs'
import { LiveBadge } from '@/components/LiveBadge'
import { Button } from '@/components/ui/button'
import { SidebarTrigger } from '@/components/ui/sidebar'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { useLiveSummary } from '@/features/realtime/useLiveSummary'
import type { Membership, Site } from '@/lib/me'
import type { Theme } from '@/lib/theme'
import { isAnonymousTracking } from '@/lib/tracking-policy'
import { en } from '@/messages/en'
import { ui } from '@/messages/ui'

function SiteLiveBadge({ siteId, org, site, detailsAvailable }: { siteId: number; org: string; site: string; detailsAvailable: boolean }) {
  const descriptionId = useId()
  const summary = useLiveSummary(siteId)
  if (summary.isError) return null

  const description = summary.data
    ? en.liveAccuracy.badgeDescription
      .replace('{count}', String(summary.data.active_visitors))
      .replace('{thirty}', String(summary.data.visitors_last_30m))
    : ui.appShell.loadingLive

  const badge = (
    <LiveBadge
      count={summary.data?.active_visitors ?? 0}
      disableLabel={description}
      enableLabel={description}
      enabled
      label={summary.data ? (count) => `${count} live` : () => '— live'}
      muted={summary.isPending}
    />
  )
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        {detailsAvailable ? (
          <Link aria-describedby={descriptionId} params={{ org, site }} to="/app/$org/$site/realtime">
            {badge}
          </Link>
        ) : <span aria-describedby={descriptionId} tabIndex={0}>{badge}</span>}
      </TooltipTrigger>
      <TooltipContent>{description}</TooltipContent>
      <span className="sr-only" id={descriptionId}>{description}</span>
    </Tooltip>
  )
}

/** The single 44px sticky bar above every page: sidebar trigger, breadcrumbs, live badge, help and theme. */
export function AppHeader({
  membership,
  site,
  requestedSite,
  theme,
  onToggleTheme,
  onOpenHelp,
}: {
  membership: Membership
  site?: Site
  requestedSite?: string
  theme: Theme
  onToggleTheme: () => void
  onOpenHelp: () => void
}) {
  return (
    <header aria-label={en.a11y2.applicationHeader} className="sticky top-0 z-20 flex h-11 shrink-0 items-center justify-between gap-2 border-b bg-background px-4">
      <div className="flex min-w-0 items-center gap-2">
        <SidebarTrigger aria-label={en.navigation.collapse} />
        <Breadcrumbs membership={membership} />
      </div>
      <div className="flex items-center gap-1">
        {site && requestedSite ? (
          <div className="hidden sm:block">
            <SiteLiveBadge
              detailsAvailable={!isAnonymousTracking(site)}
              org={membership.org.slug}
              site={requestedSite}
              siteId={site.id}
            />
          </div>
        ) : null}
        <Button
          type="button"
          size="icon"
          variant="ghost"
          className="hidden sm:inline-flex"
          aria-label={en.shortcuts.open}
          onClick={onOpenHelp}
        >
          <CircleHelpIcon />
        </Button>
        <Button
          type="button"
          size="icon"
          variant="ghost"
          aria-label={theme === 'dark' ? en.theme.dark : en.theme.light}
          onClick={onToggleTheme}
        >
          {theme === 'dark' ? <SunIcon /> : <MoonIcon />}
        </Button>
      </div>
    </header>
  )
}
