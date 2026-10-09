import { AuthorizationCacheBoundary, authorizationScope } from '@/lib/authorization-cache'
import { AuthorizationProvider, hasCapability } from '@/lib/authorization'
import { useState } from 'react'
import { Link, Outlet, useRouterState } from '@tanstack/react-router'
import { CircleHelpIcon, MoonIcon, SunIcon } from 'lucide-react'

import { LiveBadge } from '@/components/LiveBadge'
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
import { SiteIcon } from '@/components/SiteIcon'
import { useReplayAvailability } from '@/components/replay/api'
import { useLiveSummary } from '@/features/realtime/useLiveSummary'
import { useShortcuts } from '@/lib/shortcuts'
import { useTheme } from '@/lib/theme'
import { en } from '@/messages/en'
import type { Me, Membership } from '@/routes/$org'

import { activeSite, PanelSidebar, siteSlug } from './Sidebar'

function SiteLiveBadge({ siteId, org, site }: { siteId: number; org: string; site: string }) {
  const summary = useLiveSummary(siteId)
  if (summary.isError) return null

  const description = summary.data
    ? `${summary.data.active_visitors} active now, ${summary.data.visitors_last_30m} in the last 30 minutes`
    : 'Loading live visitors'

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Link
          aria-label={description}
          params={{ org, site }}
          title={description}
          to="/$org/$site/realtime"
        >
          <LiveBadge
            count={summary.data?.active_visitors ?? 0}
            disableLabel={description}
            enableLabel={description}
            enabled
            label={summary.data ? (count) => `${count} live` : () => '— live'}
            muted={summary.isPending}
          />
        </Link>
      </TooltipTrigger>
      <TooltipContent>{description}</TooltipContent>
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
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const requestedSite = useRouterState({
    select: (state) => {
      const params = state.matches.at(-1)?.params
      return params && 'site' in params ? params.site : undefined
    },
  })
  const site = activeSite(membership, requestedSite)
  const scopedSite = membership.sites.find((item) => siteSlug(item) === requestedSite)
  const replay = useReplayAvailability(hasCapability(membership, 'replay.read') ? site?.id : undefined)

  useShortcuts({
    d: toggle,
    '?': () => setHelpOpen(true),
  })

  return (
    <AuthorizationProvider membership={membership}>
    <TooltipProvider>
      <SidebarProvider defaultOpen={sidebarOpen}>
        <PanelSidebar me={me} membership={membership} requestedSite={requestedSite} replayEnabled={replay.enabled} />
        <SidebarInset>
          <header className="flex h-14 items-center justify-between border-b px-4">
            <div className="flex min-w-0 items-center gap-2">
              <SidebarTrigger aria-label={en.navigation.collapse} />
              {site ? (
                <div className="flex min-w-0 items-center gap-2">
                  <SiteIcon iconUrl={site.icon_url} name={site.name} size={24} />
                  <span className="truncate text-sm font-medium">{site.name}</span>
                </div>
              ) : null}
            </div>
            <div className="flex items-center gap-1">
              {scopedSite && requestedSite ? (
                <SiteLiveBadge
                  org={membership.org.slug}
                  site={requestedSite}
                  siteId={scopedSite.id}
                />
              ) : null}
              <Button
                type="button"
                size="icon"
                variant="ghost"
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
          <main className="min-w-0 flex-1 p-4 md:p-6">
            <AuthorizationCacheBoundary scope={authorizationScope(me.user.id, membership)}>
              <div
                key={pathname}
                className="motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-1 motion-safe:duration-150"
              >
                <Outlet />
              </div>
            </AuthorizationCacheBoundary>
          </main>
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
