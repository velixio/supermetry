import { AuthorizationCacheBoundary, authorizationScope } from '@/lib/authorization-cache'
import { AuthorizationProvider } from '@/lib/authorization'
import { useEffect, useId, useRef, useState } from 'react'
import { Link, Outlet, useRouterState } from '@tanstack/react-router'
import { CircleHelpIcon, MoonIcon, SunIcon } from 'lucide-react'

import { isAnonymousTracking } from '@/lib/tracking-policy'
import { AnalyticsPrivacyBoundary } from './AnalyticsPrivacyBoundary'
import { LiveBadge } from '@/components/LiveBadge'
import { GeolocationCredit } from '@/components/GeolocationCredit'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { SidebarInset, SidebarProvider, SidebarTrigger } from '@/components/ui/sidebar'
import { Toaster } from '@/components/ui/sonner'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { useLiveSummary } from '@/features/realtime/useLiveSummary'
import { cn } from '@/lib/utils'
import { Breadcrumbs } from './Breadcrumbs'
import { ProjectTabs } from './ProjectTabs'
import { useShortcuts } from '@/lib/shortcuts'
import { useTheme } from '@/lib/theme'
import { en } from '@/messages/en'
import type { Me, Membership } from '@/lib/me'

import { matchesSite, SupermetrySidebar } from './Sidebar'
import { DemoBanner } from '@/features/demo/DemoBanner'
import { RemoveDemoBanner } from '@/features/demo/RemoveDemoBanner'
import { privateRequest } from '@/features/settings/privateApi'
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

function sidebarStartsOpen(): boolean {
  return !document.cookie
    .split('; ')
    .some((entry) => entry === 'sidebar_state=false')
}

export function AppShell({
  me,
  membership,
}: {
  me: Me
  membership: Membership
}) {
  const { theme, toggle } = useTheme()
  const [helpOpen, setHelpOpen] = useState(false)
  const [sidebarOpen] = useState(sidebarStartsOpen)
  const { pathname, requestedSite, renderedPathnames } = useRouterState({
    select: (state) => {
      const params = state.matches.at(-1)?.params
      return {
        pathname: state.location.pathname,
        requestedSite: params && 'site' in params ? params.site : undefined,
        // Location advances before Outlet commits its active matches. Cover
        // both snapshots so back/forward cannot briefly mount a blocked page.
        renderedPathnames: [state.resolvedLocation?.pathname ?? '', ...state.matches.map((match) => match.routeId)],
      }
    },
  })
  const scopedSite = membership.sites.find((item) => matchesSite(item, requestedSite))
  const recordedLastSite = useRef(me.preferences?.last_site_id)

  useEffect(() => {
    if (!scopedSite || recordedLastSite.current === scopedSite.id) return
    recordedLastSite.current = scopedSite.id
    void privateRequest('PATCH', '/api/private/me', { body: { preferences: { last_site_id: scopedSite.id } } }).catch(() => undefined)
  }, [scopedSite])

  useShortcuts({
    d: toggle,
    '?': () => setHelpOpen(true),
  })

  return (
    <AuthorizationProvider membership={membership}>
    <TooltipProvider>
      <SidebarProvider defaultOpen={sidebarOpen}>
        <a
          className="absolute -left-[10000px] top-auto h-px w-px overflow-hidden whitespace-nowrap focus:fixed focus:left-4 focus:top-4 focus:z-[100] focus:h-auto focus:w-auto focus:overflow-visible focus:rounded-full focus:bg-background focus:px-4 focus:py-2 focus:text-sm focus:font-medium focus:text-foreground focus:shadow-lg focus:ring-2 focus:ring-ring focus:ring-offset-2 focus:ring-offset-background"
          href="#main-content"
          onClick={(event) => {
            event.preventDefault()
            document.getElementById('main-content')?.focus()
          }}
        >
          {en.a11y2.skipToMain}
        </a>
        <SupermetrySidebar me={me} membership={membership} requestedSite={requestedSite} />
        <SidebarInset>
          {membership.sites.length > 0 ? <ProjectTabs membership={membership} activeSiteId={scopedSite?.id} /> : null}
          <header aria-label={en.a11y2.applicationHeader} className={cn('sticky z-20 flex h-14 items-center justify-between gap-2 border-b bg-background px-4', membership.sites.length > 0 ? 'top-9' : 'top-0')}>
            <div className="flex min-w-0 items-center gap-2">
              <SidebarTrigger aria-label={en.navigation.collapse} />
              <Breadcrumbs membership={membership} />
            </div>
            <div className="flex items-center gap-1">
              {scopedSite && requestedSite ? (
                <div className="hidden sm:block"><SiteLiveBadge
                  org={membership.org.slug}
                  site={requestedSite}
                  siteId={scopedSite.id}
                  detailsAvailable={!isAnonymousTracking(scopedSite)}
                /></div>
              ) : null}
              <Button
                type="button"
                size="icon"
                variant="ghost"
                className="hidden sm:inline-flex"
                aria-label={en.shortcuts.open}
                onClick={() => setHelpOpen(true)}
              >
                <CircleHelpIcon />
              </Button>
              <Button
                type="button"
                size="icon"
                variant="ghost"
                aria-label={theme === 'dark' ? en.theme.dark : en.theme.light}
                onClick={toggle}
              >
                {theme === 'dark' ? <SunIcon /> : <MoonIcon />}
              </Button>
            </div>
          </header>
          <main aria-label={en.a11y2.mainContent} className="min-w-0 flex-1 p-4 md:p-6" id="main-content" tabIndex={-1}>
            {scopedSite?.is_demo ? (
              <DemoBanner org={membership.org} site={scopedSite} />
            ) : (
              <RemoveDemoBanner currentSite={scopedSite} membership={membership} />
            )}
            <AuthorizationCacheBoundary scope={authorizationScope(me.user.id, membership)}>
              <div
                key={pathname}
                className="motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-1 motion-safe:duration-150"
              >
                <AnalyticsPrivacyBoundary site={scopedSite} org={membership.org.slug} requestedSite={requestedSite} pathname={pathname} renderedPathnames={renderedPathnames}>
                  <Outlet />
                </AnalyticsPrivacyBoundary>
              </div>
            </AuthorizationCacheBoundary>
          </main>
          <footer className="px-4 pb-4 text-center text-xs text-muted-foreground md:px-6">
            <GeolocationCredit />
          </footer>
        </SidebarInset>
      </SidebarProvider>
      <Dialog open={helpOpen} onOpenChange={setHelpOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{en.shortcuts.title}</DialogTitle>
            <DialogDescription>{en.shortcuts.description}</DialogDescription>
          </DialogHeader>
          <ul className="flex flex-col gap-2 text-sm">
            <li>{en.shortcuts.theme}</li>
            <li>{en.shortcuts.help}</li>
          </ul>
        </DialogContent>
      </Dialog>
      <Toaster />
    </TooltipProvider>
    </AuthorizationProvider>
  )
}
