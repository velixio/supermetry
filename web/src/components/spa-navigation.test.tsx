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

import type { Me, Membership } from '@/routes/app.$org'

import { SupermetrySidebar } from './Sidebar'
import { RoutePending } from './RoutePending'
import { SidebarProvider } from './ui/sidebar'
import { TooltipProvider } from './ui/tooltip'

afterEach(cleanup)

const membership: Membership = {
  org: { id: 1, name: 'Acme', slug: 'acme' },
  role: 'owner',
  effective_capabilities: ['profiles.read', 'replay.read'],
  sites: [{ id: 1, name: 'Docs', public_id: 'docs-public', tracking_policy: { instance_mode: 'identified', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }],
}

const me: Me = {
  user: { id: 1, name: 'Owner', email: 'owner@supermetry.test' },
  memberships: [membership],
}

function renderSidebar(initialEntry = '/app/acme/docs-public/realtime') {
  const rootRoute = createRootRoute({
    component: () => (
      <TooltipProvider>
        <SidebarProvider>
          <SupermetrySidebar me={me} membership={membership} requestedSite="docs-public" />
          <Outlet />
        </SidebarProvider>
      </TooltipProvider>
    ),
  })
  const appRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: 'app',
    component: Outlet,
  })
  const siteRoute = createRoute({
    getParentRoute: () => appRoute,
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
      appRoute.addChildren([siteRoute.addChildren([siteIndexRoute, siteSectionRoute])]),
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

    await waitFor(() => expect(router.state.location.pathname).toBe('/app/acme/docs-public'))
    expect(screen.getByRole('heading', { name: 'Dashboard page' })).toBeInTheDocument()
  })

  it('places permission-gated Replays immediately after Realtime without a Map destination', async () => {
    renderSidebar()

    const realtime = await screen.findByRole('link', { name: 'Realtime' })
    const replays = screen.getByRole('link', { name: 'Replays' })
    expect(realtime.closest('li')?.nextElementSibling).toBe(replays.closest('li'))
    expect(screen.queryByRole('link', { name: 'Map' })).not.toBeInTheDocument()
  })

  it('shows the brand name and one dropdown listing projects, then organizations', async () => {
    renderSidebar()

    expect(await screen.findByText('Supermetry')).toBeInTheDocument()
    const trigger = screen.getByRole('button', { name: /Docs\s*Acme/ })
    fireEvent.pointerDown(trigger, { button: 0, ctrlKey: false, pointerType: 'mouse' })

    const menu = await screen.findByRole('menu')
    const text = menu.textContent ?? ''
    expect(text.indexOf('Projects')).toBeGreaterThanOrEqual(0)
    expect(text.indexOf('Projects')).toBeLessThan(text.indexOf('Docs'))
    expect(text.indexOf('Docs')).toBeLessThan(text.indexOf('Organizations'))
    expect(text.indexOf('Organizations')).toBeLessThan(text.lastIndexOf('Acme'))
    expect(screen.getByRole('menuitem', { name: /Add organization/ })).toHaveAttribute('href', '/app/account/organizations/new')
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
