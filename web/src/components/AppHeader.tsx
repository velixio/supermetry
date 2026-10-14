import { useId, type Ref } from 'react'
import { Link } from '@tanstack/react-router'
import { CircleHelpIcon, MoonIcon, SearchIcon, SunIcon } from 'lucide-react'

import { Breadcrumbs } from './Breadcrumbs'
import { LanguageMenu } from './LanguageMenu'
import { LiveBadge } from '@/components/LiveBadge'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { SidebarTrigger } from '@/components/ui/sidebar'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { useLiveSummary } from '@/features/realtime/useLiveSummary'
import { useUpdateStatus } from '@/features/admin/adminApi'
import type { Membership, Site } from '@/lib/me'
import type { LocaleCode } from '@/lib/i18n'
import type { Theme } from '@/lib/theme'
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
 * The single 44px sticky bar above every page: sidebar trigger, breadcrumbs, command palette trigger, live badge, help and theme.
 * Outside an organization (no membership) there is no site, so no live badge.
 */
export function AppHeader({
  instanceAdmin = false,
  membership,
  site,
  requestedSite,
  locale = '',
  theme,
  onToggleTheme,
  onOpenHelp,
  onOpenPalette,
  paletteTriggerRef,
}: {
  instanceAdmin?: boolean
  membership?: Membership
  site?: Site
  requestedSite?: string
  /** The signed-in user's saved interface language; '' follows the browser. */
  locale?: LocaleCode | ''
  theme: Theme
  onToggleTheme: () => void
  onOpenHelp: () => void
  onOpenPalette: () => void
  /** Lets the shell return focus here after a command that leaves focus nowhere else. */
  paletteTriggerRef?: Ref<HTMLButtonElement>
}) {
  return (
    <header aria-label={en.a11y2.applicationHeader} className="sticky top-0 z-20 flex h-11 shrink-0 items-center justify-between gap-2 border-b bg-background px-4">
      <div className="flex min-w-0 items-center gap-2">
        <SidebarTrigger aria-label={en.navigation.collapse} />
        <Breadcrumbs membership={membership} />
      </div>
      <div className="flex items-center gap-1">
        <Button
          ref={paletteTriggerRef}
          type="button"
          size="sm"
          variant="ghost"
          className="gap-1.5 text-muted-foreground"
          aria-label={ui.palette.open}
          aria-keyshortcuts="Control+K Meta+K"
          onClick={onOpenPalette}
        >
          <SearchIcon aria-hidden="true" />
          <kbd className="hidden rounded border bg-muted px-1 font-sans text-[0.7rem] font-medium md:inline">
            {isApplePlatform() ? ui.palette.keyHintApple : ui.palette.keyHint}
          </kbd>
        </Button>
        <UpdateNotice instanceAdmin={instanceAdmin} />
        {membership && site && requestedSite ? (
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
        <LanguageMenu locale={locale} />
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
