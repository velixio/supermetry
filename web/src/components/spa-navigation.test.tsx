import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import type { Me, Membership } from '@/routes/$org'

import { PanelSidebar } from './Sidebar'
import { RoutePending } from './RoutePending'
import { SidebarProvider } from './ui/sidebar'
import { TooltipProvider } from './ui/tooltip'

afterEach(cleanup)

const membership: Membership = {
  org: { id: 1, name: 'Acme', slug: 'acme' },
  role: 'owner',
  sites: [{ id: 1, name: 'Docs', public_id: 'docs-public' }],
}

const me: Me = {
  user: { id: 1, name: 'Owner', email: 'owner@panel.test' },
  memberships: [membership],
}

function renderSidebar(initialEntry = '/acme/docs-public/realtime') {
  const rootRoute = createRootRoute({
    component: () => (
      <TooltipProvider>
        <SidebarProvider>
          <PanelSidebar me={me} membership={membership} requestedSite="docs-public" />
          <Outlet />
        </SidebarProvider>
      </TooltipProvider>
    ),
  })
  const siteRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '$org/$site',
    component: () => <Outlet />,
  })
  const siteIndexRoute = createRoute({
    getParentRoute: () => siteRoute,
    path: '/',
    component: () => <h1>Dashboard page</h1>,
  })
  const siteSectionRoute = createRoute({
    getParentRoute: () => siteRoute,
    path: '$section',
    component: () => <h1>Section page</h1>,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([
      siteRoute.addChildren([siteIndexRoute, siteSectionRoute]),
    ]),
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })

  render(<RouterProvider router={router} />)
  return router
}

describe('SPA navigation', () => {
  beforeEach(() => {
    window.scrollTo = () => undefined
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: () => ({
        matches: false,
        addEventListener: () => undefined,
        removeEventListener: () => undefined,
      }),
    })
  })

  it('uses the client router for sidebar links and exposes the active route', async () => {
    const router = renderSidebar()

    const realtime = await screen.findByRole('link', { name: 'Realtime' })
    expect(realtime).toHaveAttribute('aria-current', 'page')

    fireEvent.click(screen.getByRole('link', { name: 'Dashboard' }))

    await waitFor(() => expect(router.state.location.pathname).toBe('/acme/docs-public'))
    expect(screen.getByRole('heading', { name: 'Dashboard page' })).toBeInTheDocument()
  })

  it('renders a stable skeleton fallback for delayed route chunks', () => {
    render(<RoutePending />)

    expect(screen.getByRole('status', { name: 'Loading page' })).toHaveAttribute(
      'aria-busy',
      'true',
    )
    expect(document.querySelectorAll('[data-slot="skeleton"]')).not.toHaveLength(0)
  })
})
import '@testing-library/jest-dom/vitest'
