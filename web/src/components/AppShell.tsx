import { useState } from 'react'
import { Outlet, useRouterState } from '@tanstack/react-router'
import { CircleHelpIcon, MoonIcon, SunIcon } from 'lucide-react'

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
import { TooltipProvider } from '@/components/ui/tooltip'
import { useShortcuts } from '@/lib/shortcuts'
import { useTheme } from '@/lib/theme'
import { en } from '@/messages/en'
import type { Me, Membership } from '@/routes/$org'

import { PanelSidebar } from './Sidebar'

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
  const requestedSite = pathname.split('/').filter(Boolean)[1]

  useShortcuts({
    d: toggle,
    '?': () => setHelpOpen(true),
  })

  return (
    <TooltipProvider>
      <SidebarProvider defaultOpen={sidebarOpen}>
        <PanelSidebar me={me} membership={membership} requestedSite={requestedSite} />
        <SidebarInset>
          <header className="flex h-14 items-center justify-between border-b px-4">
            <SidebarTrigger aria-label={en.navigation.collapse} />
            <div className="flex items-center gap-1">
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
            <div
              key={pathname}
              className="motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-1 motion-safe:duration-150"
            >
              <Outlet />
            </div>
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
  )
}
