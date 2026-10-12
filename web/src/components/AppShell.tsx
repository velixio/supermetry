import { AuthorizationCacheBoundary, authorizationScope } from '@/lib/authorization-cache'
import { AuthorizationProvider } from '@/lib/authorization'
import { lazy, Suspense, useEffect, useRef, useState } from 'react'
import { Outlet, useRouter, useRouterState } from '@tanstack/react-router'

import { AccountSidebar } from './AccountSidebar'
import { AnalyticsPrivacyBoundary } from './AnalyticsPrivacyBoundary'
import { AppHeader } from './AppHeader'
import { GeolocationCredit } from '@/components/GeolocationCredit'
import { PoweredBySupermetry } from '@/components/PoweredBySupermetry'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar'
import { Toaster } from '@/components/ui/sonner'
import { TooltipProvider } from '@/components/ui/tooltip'
import { useShortcuts } from '@/lib/shortcuts'
import { useTheme } from '@/lib/theme'
import { en } from '@/messages/en'
import type { Me, Membership } from '@/lib/me'

import { matchesSite, SupermetrySidebar } from './Sidebar'
import { DemoBanner } from '@/features/demo/DemoBanner'
import { RemoveDemoBanner } from '@/features/demo/RemoveDemoBanner'
import { privateRequest } from '@/features/settings/privateApi'
import { requestLogout } from '@/lib/session-lifecycle'
import { paletteCommands, type PaletteCommand } from './paletteCommands'

// cmdk and the palette load on first open, so they stay out of the initial JS budget.
const CommandPalette = lazy(() => import('./CommandPalette').then((module) => ({ default: module.CommandPalette })))
import { ui } from '@/messages/ui'
import { sidebarStartsOpen } from './sidebarState'

export function AppShell({
  me,
  membership,
}: {
  me: Me
  membership?: Membership
}) {
  const { theme, toggle } = useTheme()
  const [helpOpen, setHelpOpen] = useState(false)
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [paletteLoaded, setPaletteLoaded] = useState(false)
  const paletteTrigger = useRef<HTMLButtonElement>(null)
  const router = useRouter()
  const [sidebarOpen] = useState(() => sidebarStartsOpen(document.cookie, window.innerWidth))
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
  const scopedSite = membership?.sites.find((item) => matchesSite(item, requestedSite))
  const recordedLastSite = useRef(me.preferences?.last_site_id)

  useEffect(() => {
    if (!scopedSite || recordedLastSite.current === scopedSite.id) return
    recordedLastSite.current = scopedSite.id
    void privateRequest('PATCH', '/api/private/me', { body: { preferences: { last_site_id: scopedSite.id } } }).catch(() => undefined)
  }, [scopedSite])

  const openPalette = () => {
    setPaletteLoaded(true)
    setPaletteOpen(true)
  }
  // Runs after the palette starts closing; the palette decides where focus goes afterwards.
  const runPaletteCommand = (command: PaletteCommand) => {
    setPaletteOpen(false)
    if (command.kind === 'navigate') router.history.push(command.href)
    else if (command.action === 'theme') toggle()
    else if (command.action === 'help') setHelpOpen(true)
    else void requestLogout()
  }

  useShortcuts({
    d: toggle,
    '?': () => setHelpOpen(true),
    'mod+k': () => {
      setPaletteLoaded(true)
      setPaletteOpen((open) => !open)
    },
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
        {membership ? (
          <SupermetrySidebar me={me} membership={membership} requestedSite={requestedSite} />
        ) : (
          <AccountSidebar me={me} />
        )}
        <SidebarInset>
          <AppHeader
            membership={membership}
            site={scopedSite}
            requestedSite={requestedSite}
            theme={theme}
            onToggleTheme={toggle}
            onOpenHelp={() => setHelpOpen(true)}
            onOpenPalette={openPalette}
            paletteTriggerRef={paletteTrigger}
          />
          <main aria-label={en.a11y2.mainContent} className="min-w-0 flex-1 p-4 md:p-6" id="main-content" tabIndex={-1}>
            {membership ? (
              scopedSite?.is_demo ? (
                <DemoBanner org={membership.org} site={scopedSite} />
              ) : (
                <RemoveDemoBanner currentSite={scopedSite} membership={membership} />
              )
            ) : null}
            <AuthorizationCacheBoundary scope={authorizationScope(me.user.id, membership)}>
              <div
                key={pathname}
                className="motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-1 motion-safe:duration-150"
              >
                {membership ? (
                  <AnalyticsPrivacyBoundary site={scopedSite} org={membership.org.slug} requestedSite={requestedSite} pathname={pathname} renderedPathnames={renderedPathnames}>
                    <Outlet />
                  </AnalyticsPrivacyBoundary>
                ) : (
                  <Outlet />
                )}
              </div>
            </AuthorizationCacheBoundary>
          </main>
          <footer className="flex flex-wrap items-center justify-center gap-x-3 px-4 pb-4 text-center text-xs text-muted-foreground md:px-6">
            <GeolocationCredit />
            <PoweredBySupermetry className="hover:text-foreground" />
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
            <li>{en.shortcuts.sidebar}</li>
            <li>{en.shortcuts.palette}</li>
            <li>{en.shortcuts.period}</li>
            <li>{en.shortcuts.compare}</li>
          </ul>
        </DialogContent>
      </Dialog>
      {paletteLoaded ? (
        <Suspense fallback={null}>
          <CommandPalette
            open={paletteOpen}
            onOpenChange={setPaletteOpen}
            commands={paletteOpen ? paletteCommands({ me, membership, requestedSite, theme }) : []}
            onRun={runPaletteCommand}
            fallbackFocus={() => paletteTrigger.current}
          />
        </Suspense>
      ) : null}
      <Toaster />
    </TooltipProvider>
    </AuthorizationProvider>
  )
}
