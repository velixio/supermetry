import { useId, type Ref } from 'react'
import { Link } from '@tanstack/react-router'
import { SearchIcon } from 'lucide-react'

import { Breadcrumbs } from './Breadcrumbs'
import { HelpMenu } from './HelpMenu'
import { LiveBadge } from '@/components/LiveBadge'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { SidebarTrigger } from '@/components/ui/sidebar'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { useLiveSummary } from '@/features/realtime/useLiveSummary'
import { useUpdateStatus } from '@/features/admin/adminApi'
import type { Membership, Site } from '@/lib/me'
import { isApplePlatform } from '@/lib/shortcuts'
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

function UpdateNotice({ instanceAdmin }: { instanceAdmin: boolean }) {
  const status = useUpdateStatus(instanceAdmin)
  const jobStatus = status.data?.job?.status
  const updateActive = Boolean(jobStatus && !['complete', 'failed', 'recovery_required'].includes(jobStatus))
  if (!instanceAdmin || status.isError || !updateActive && !status.data?.update_available) return null
  return (
    <span role="status" aria-atomic="true">
      <Badge asChild variant="secondary" className="min-h-6">
        <Link to="/app/admin/update">{updateActive ? en.admin.updateInProgress : en.admin.updateAvailable}</Link>
      </Badge>
    </span>
  )
}

/**
 * The single 48px sticky bar above every page: sidebar trigger, breadcrumbs, search field (opens the command palette), live badge and help.
 * Language and theme live in the account menu.
 * Outside an organization (no membership) there is no site, so no live badge.
 */
export function AppHeader({
  instanceAdmin = false,
  membership,
  site,
  requestedSite,
  onOpenHelp,
  onOpenAbout,
  onOpenPalette,
  paletteTriggerRef,
}: {
  instanceAdmin?: boolean
  membership?: Membership
  site?: Site
  requestedSite?: string
  onOpenHelp: () => void
  onOpenAbout: () => void
  onOpenPalette: () => void
  /** Lets the shell return focus here after a command that leaves focus nowhere else. */
  paletteTriggerRef?: Ref<HTMLButtonElement>
}) {
  return (
    <header aria-label={en.a11y2.applicationHeader} className="sticky top-0 z-20 flex h-12 shrink-0 items-center justify-between gap-2 border-b bg-background px-4">
      <div className="flex min-w-0 items-center gap-2">
        <SidebarTrigger aria-label={en.navigation.collapse} />
        <Breadcrumbs membership={membership} />
      </div>
      <div className="flex items-center gap-1">
        {/* One search field for the command palette. Its accessible name is its visible text; the
            shortcut hint sits inside the field and is announced through aria-keyshortcuts. */}
        <Button
          ref={paletteTriggerRef}
          type="button"
          variant="outline"
          className="justify-start gap-2 border-border bg-muted/50 px-3 font-normal text-muted-foreground shadow-none max-md:w-9 max-md:justify-center max-md:border-transparent max-md:bg-transparent max-md:dark:bg-transparent max-md:dark:hover:bg-accent max-md:px-0 max-md:hover:bg-accent md:w-64 lg:w-72"
          aria-haspopup="dialog"
          aria-keyshortcuts="Control+K Meta+K"
          aria-label={ui.palette.searchField}
          onClick={onOpenPalette}
        >
          <SearchIcon aria-hidden="true" />
          <span aria-hidden="true" className="hidden flex-1 truncate text-left md:inline">{ui.palette.searchField}</span>
          <kbd aria-hidden="true" className="pointer-events-none hidden rounded border bg-background px-1.5 py-0.5 font-sans text-xs font-medium text-muted-foreground md:inline">
            {isApplePlatform() ? ui.palette.keyHintApple : ui.palette.keyHint}
          </kbd>
        </Button>
        <UpdateNotice instanceAdmin={instanceAdmin} />
        {membership && site && requestedSite && site.kind !== 'app' ? (
          <div className="hidden sm:block">
            <SiteLiveBadge
              detailsAvailable={!isAnonymousTracking(site)}
              org={membership.org.slug}
              site={requestedSite}
              siteId={site.id}
            />
          </div>
        ) : null}
        <HelpMenu onOpenAbout={onOpenAbout} onOpenShortcuts={onOpenHelp} />
      </div>
    </header>
  )
}
