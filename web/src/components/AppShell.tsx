import { AuthorizationCacheBoundary, authorizationScope } from '@/lib/authorization-cache'
import { AuthorizationProvider } from '@/lib/authorization'
import { lazy, Suspense, useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'
import { Outlet, useRouter, useRouterState } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'

import { AccountSidebar } from './AccountSidebar'
import { AnalyticsPrivacyBoundary } from './AnalyticsPrivacyBoundary'
import { AppHeader } from './AppHeader'
import { AboutDialog } from '@/components/AboutDialog'
import { ImpersonationBanner } from '@/components/ImpersonationBanner'
import { ShellPrefsContext } from '@/components/ShellPrefsContext'
import { MAIN_CONTENT_ID, SkipLink } from '@/components/SkipLink'
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
import { useManagedState } from '@/lib/instanceConfig'
import { useShortcuts } from '@/lib/shortcuts'
import { useTheme } from '@/lib/theme'
import { en } from '@/messages/en'
import { siteNeedsDetails, type Me, type Membership } from '@/lib/me'

import { matchesSite, SupermetrySidebar, useCurrentRoute } from './Sidebar'
import { DemoBanner } from '@/features/demo/DemoBanner'
import { getSite } from '@/features/sites/api'
import { RemoveDemoBanner } from '@/features/demo/RemoveDemoBanner'
import { privateRequest } from '@/features/settings/privateApi'
import { requestLogout } from '@/lib/session-lifecycle'
import { readRecentCommands, recordRecentCommand } from '@/lib/shellPrefs'
import { paletteCommands, type PaletteCommand } from './paletteCommands'
import { ui } from '@/messages/ui'
import { sidebarStartsOpen } from './sidebarState'
import { loadPalette } from './paletteLoader'
import { PaletteBoundary } from './PaletteBoundary'
import { InAppShell, UnknownSite } from './NotFound'

// cmdk and the palette load on first open, so they stay out of the initial JS budget.
const lazyPalette = () => lazy(loadPalette)

export function AppShell({
  me,
  membership,
}: {
  me: Me
  membership?: Membership
}) {
  const { theme, toggle } = useTheme()
  const { managed, known: managedKnown } = useManagedState()
  const [helpOpen, setHelpOpen] = useState(false)
  const [aboutOpen, setAboutOpen] = useState(false)
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [paletteLoaded, setPaletteLoaded] = useState(false)
  // React.lazy memoises a rejected import, so a failed load gets a fresh lazy component and boundary.
  // lazyPalette is an initializer here and an updater in onError: each call yields a fresh lazy component.
  const [Palette, setPalette] = useState(lazyPalette)
  const [paletteAttempt, setPaletteAttempt] = useState(0)
  const paletteTrigger = useRef<HTMLButtonElement>(null)
  const router = useRouter()
  const [sidebarOpen] = useState(() => sidebarStartsOpen(document.cookie, window.innerWidth))
  // The palette's switch commands keep the current page, like the sidebar switchers.
  const currentRoute = useCurrentRoute()
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
  // Commands are computed only while open; the last list is kept while the dialog animates closed
  // (an empty list would flash "no matching commands"), and dropped when the scope changes.
  const paletteScope = `${membership?.org.id ?? ''}/${requestedSite ?? ''}`
  const lastCommands = useRef<{ scope: string; list: PaletteCommand[] }>({ scope: '', list: [] })
  if (paletteOpen) lastCommands.current = { scope: paletteScope, list: paletteCommands({
      me, membership, requestedSite, theme, current: currentRoute, managed,
      // Settings destinations come from the route tree, so a new settings route appears without a palette change.
      routePaths: Object.keys(router.routesByPath ?? {}),
      recent: readRecentCommands(me.user.id),
    }) }
  const commands = lastCommands.current.scope === paletteScope ? lastCommands.current.list : []
  const scopedSite = membership?.sites.find((item) => matchesSite(item, requestedSite))
  const siteDetails = useQuery({
    queryKey: ['site', scopedSite?.id],
    queryFn: () => getSite(scopedSite!.id, membership!.org.id),
    enabled: Boolean(scopedSite && membership && siteNeedsDetails(scopedSite.kind)),
  })
  const resolvedSite = siteDetails.data?.id === scopedSite?.id ? siteDetails.data : scopedSite
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
    recordRecentCommand(me.user.id, command.id)
    if (command.kind === 'navigate') router.history.push(command.href)
    else if (command.action === 'theme') toggle()
    else if (command.action === 'help') setHelpOpen(true)
    else if (command.action === 'about') setAboutOpen(true)
    else void requestLogout()
  }

  useShortcuts({
    d: toggle,
    '?': () => setHelpOpen(true),
    'mod+k': () => {
      if (helpOpen) return
      setPaletteLoaded(true)
      setPaletteOpen((open) => !open)
    },
  })

  return (
    <AuthorizationProvider membership={membership}>
    <InAppShell.Provider value>
    <TooltipProvider>
      <ShellPrefsContext.Provider value={{ theme, toggleTheme: toggle }}>
      <SidebarProvider defaultOpen={sidebarOpen}>
        <SkipLink
          onClick={(event) => {
            event.preventDefault()
            document.getElementById(MAIN_CONTENT_ID)?.focus()
          }}
        >
          {en.a11y2.skipToMain}
        </SkipLink>
        {membership ? (
          <SupermetrySidebar me={me} membership={membership} requestedSite={requestedSite} resolvedSite={resolvedSite} />
        ) : (
          <AccountSidebar me={me} />
        )}
        <SidebarInset>
          <AppHeader
            instanceAdmin={Boolean(me.user.instance_admin) && managedKnown && !managed}
            membership={membership}
            site={resolvedSite}
            requestedSite={requestedSite}
            onOpenHelp={() => setHelpOpen(true)}
            onOpenAbout={() => setAboutOpen(true)}
            onOpenPalette={openPalette}
            paletteTriggerRef={paletteTrigger}
          />
          <main aria-label={en.a11y2.mainContent} className="min-w-0 flex-1 px-4 py-3 md:px-6 md:py-4" id={MAIN_CONTENT_ID} tabIndex={-1}>
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
                {membership && requestedSite && !scopedSite ? (
                  <UnknownSite key={`${membership.org.id}/${requestedSite}`} membership={membership} />
                ) : membership ? (
                  <AnalyticsPrivacyBoundary site={scopedSite} org={membership.org.slug} requestedSite={requestedSite} pathname={pathname} renderedPathnames={renderedPathnames}>
                    <Outlet />
                  </AnalyticsPrivacyBoundary>
                ) : (
                  <Outlet />
                )}
              </div>
            </AuthorizationCacheBoundary>
          </main>
          <ImpersonationBanner me={me} />
        </SidebarInset>
      </SidebarProvider>
      <AboutDialog open={aboutOpen} onOpenChange={setAboutOpen} version={me.version} />
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
        <PaletteBoundary
          key={paletteAttempt}
          onError={() => {
            setPaletteOpen(false)
            // Unload too, so the failed load is retried on the next open, not straight away.
            setPaletteLoaded(false)
            setPalette(lazyPalette)
            setPaletteAttempt((attempt) => attempt + 1)
            toast.error(ui.palette.loadFailed)
          }}
        >
          <Suspense fallback={null}>
            <Palette
              open={paletteOpen}
              onOpenChange={setPaletteOpen}
              commands={commands}
              onRun={runPaletteCommand}
              fallbackFocus={() => paletteTrigger.current}
            />
          </Suspense>
        </PaletteBoundary>
      ) : null}
      <Toaster />
      </ShellPrefsContext.Provider>
    </TooltipProvider>
    </InAppShell.Provider>
    </AuthorizationProvider>
  )
}
